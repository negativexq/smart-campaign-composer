//go:build load

package load_test

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

// What the provider does with a call.
const (
	answerAccept = "accept"
	answer429    = "429"
	answer500    = "500"
	// answerLost accepts the message and never answers, as when a response is lost.
	answerLost = "lost"
)

// provider is a gateway whose behaviour a scenario fully controls. It keeps every
// arrival, because the limit has to be judged by what the provider saw.
type provider struct {
	// decide answers the nth call on a channel, counting from 1.
	decide func(channel string, call int) string
	// network is the delay before a call counts as arrived. It imitates the way there.
	network func() time.Duration
	// latency is how long the provider takes to answer.
	latency func() time.Duration
	// quota answers 429 to calls beyond this many in any second, on each channel. Zero means none.
	quota int

	mu       sync.Mutex
	calls    map[string]int
	arrivals map[string][]time.Time
	// timed keeps when each call left the worker, as well as when it counted as arrived.
	timed    map[string][]timedCall
	accepts  map[string][]time.Time
	answers  map[string]map[string]int
	known    map[string]string
	accepted map[string]int
	repeats  int
	// abandoned counts arrivals whose caller was gone before they were handled.
	abandoned int
}

// timedCall is one call's way to the provider. Workers and provider share a machine, so
// sent, taken as the call reaches the handler, is the moment the worker sent it.
type timedCall struct {
	sent, arrived time.Time
	delayed       bool
}

func newProvider() *provider {
	return &provider{
		decide:   func(string, int) string { return answerAccept },
		network:  func() time.Duration { return 0 },
		latency:  func() time.Duration { return 0 },
		calls:    map[string]int{},
		arrivals: map[string][]time.Time{},
		timed:    map[string][]timedCall{},
		accepts:  map[string][]time.Time{},
		answers:  map[string]map[string]int{},
		known:    map[string]string{},
		accepted: map[string]int{},
	}
}

func (p *provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sent := time.Now()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	var message struct {
		Channel string `json:"channel"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	delay := p.network()
	if !wait(r, delay) {
		return
	}
	answer := p.arrive(message.Channel, sent, delay > 0)
	if !wait(r, p.latency()) {
		// The caller went away before the provider did anything with the call.
		p.mu.Lock()
		p.abandoned++
		p.mu.Unlock()
		return
	}
	switch answer {
	case answer429:
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	case answer500:
		w.WriteHeader(http.StatusInternalServerError)
	case answerLost:
		p.accept(message.Channel, r.Header.Get("Idempotency-Key"))
		<-r.Context().Done()
	default:
		id, repeat := p.accept(message.Channel, r.Header.Get("Idempotency-Key"))
		status := http.StatusAccepted
		if repeat {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "duplicate": repeat})
	}
}

func wait(r *http.Request, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	select {
	case <-r.Context().Done():
		return false
	case <-time.After(delay):
		return true
	}
}

func (p *provider) arrive(channel string, sent time.Time, delayed bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.calls[channel]++
	p.arrivals[channel] = append(p.arrivals[channel], now)
	p.timed[channel] = append(p.timed[channel], timedCall{sent: sent, arrived: now, delayed: delayed})
	answer := p.decide(channel, p.calls[channel])
	if p.quota > 0 && answer == answerAccept && p.overQuota(channel, now) {
		answer = answer429
	}
	if p.answers[channel] == nil {
		p.answers[channel] = map[string]int{}
	}
	p.answers[channel][answer]++
	return answer
}

// overQuota asks whether this call makes more than the quota in the last second.
func (p *provider) overQuota(channel string, now time.Time) bool {
	arrivals := p.arrivals[channel]
	return len(arrivals) > p.quota && now.Sub(arrivals[len(arrivals)-1-p.quota]) < time.Second
}

// accept remembers a key as a gateway does. A key it has forgotten is a new message,
// which is what counts as a person getting it twice.
func (p *provider) accept(channel, key string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, found := p.known[key]; found {
		p.repeats++
		return id, true
	}
	id := "load-" + strconv.Itoa(len(p.known)+1)
	p.known[key] = id
	p.accepted[key]++
	p.accepts[channel] = append(p.accepts[channel], time.Now())
	return id, false
}

// restart forgets every key, as a gateway that keeps them only in memory does.
func (p *provider) restart() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.known = map[string]string{}
}

func (p *provider) callCount(channel string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[channel]
}

// channelFigures is what the provider saw on one channel.
type channelFigures struct {
	Requests       int            `json:"requests"`
	Answers        map[string]int `json:"answers"`
	UniqueAccepted int            `json:"unique_accepted"`
	PeakPerSecond  int            `json:"peak_in_any_second"`
	OverLimitCalls int            `json:"calls_over_the_limit"`
	RequestRate    float64        `json:"request_rate"`
	AcceptedRate   float64        `json:"accepted_rate"`
	LongestGap     float64        `json:"longest_gap_seconds"`
	SpanSeconds    float64        `json:"span_seconds"`
	RetryOverhead  float64        `json:"retry_overhead"`
	CompletedRate  float64        `json:"completed_rate"`
	Statuses       map[string]int `json:"statuses"`
	AttemptsUsed   int            `json:"attempts_used"`
	// PeakSent is the same peak counted when calls left the workers, so the difference
	// from PeakPerSecond is what the way to the provider did.
	PeakSent int `json:"peak_sent_in_any_second"`
	// Peak100ms shows bursts, which a count per second hides.
	Peak100ms int `json:"peak_in_any_100ms"`
	// AfterGap is what came straight after the longest silence: a catch-up would show here.
	AfterGap100ms  int            `json:"calls_in_100ms_after_longest_gap"`
	AfterGapSecond int            `json:"calls_in_second_after_longest_gap"`
	SendGaps       map[string]int `json:"gaps_between_sends"`
	SendGroups     map[string]int `json:"sends_within_2ms_of_each_other"`
	Peak           *peakWindow    `json:"busiest_second,omitempty"`
}

// peakWindow takes the busiest second at the provider apart.
type peakWindow struct {
	Calls       int     `json:"calls"`
	Delayed     int     `json:"delayed"`
	Undelayed   int     `json:"undelayed"`
	SentSeconds float64 `json:"seconds_those_calls_took_to_leave"`
	SentRate    float64 `json:"their_rate_when_leaving"`
}

func (p *provider) figures(channel string, limit int) channelFigures {
	p.mu.Lock()
	defer p.mu.Unlock()
	arrivals := slices.Clone(p.arrivals[channel])
	accepts := slices.Clone(p.accepts[channel])
	slices.SortFunc(arrivals, time.Time.Compare)
	slices.SortFunc(accepts, time.Time.Compare)
	peak, over := slidingPeak(arrivals, limit)
	figures := channelFigures{
		Requests: len(arrivals), Answers: map[string]int{}, UniqueAccepted: len(accepts),
		PeakPerSecond: peak, OverLimitCalls: over,
		RequestRate: middleRate(arrivals), AcceptedRate: middleRate(accepts), LongestGap: longestGap(arrivals),
	}
	for answer, count := range p.answers[channel] {
		figures.Answers[answer] = count
	}
	figures.Peak100ms = peakIn(arrivals, 100*time.Millisecond)
	figures.AfterGap100ms, figures.AfterGapSecond = afterLongestGap(arrivals)
	calls := slices.Clone(p.timed[channel])
	slices.SortFunc(calls, func(a, b timedCall) int { return a.sent.Compare(b.sent) })
	sent := make([]time.Time, len(calls))
	for i, call := range calls {
		sent[i] = call.sent
	}
	figures.PeakSent = peakIn(sent, time.Second)
	figures.SendGaps, figures.SendGroups = sendShape(sent)
	figures.Peak = busiestSecond(calls)
	if len(arrivals) > 1 {
		figures.SpanSeconds = arrivals[len(arrivals)-1].Sub(arrivals[0]).Seconds()
	}
	if len(accepts) > 0 {
		figures.RetryOverhead = float64(len(arrivals)-len(accepts)) / float64(len(accepts))
	}
	return figures
}

// duplicates counts keys the provider accepted as new more than once.
func (p *provider) duplicates() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	for _, count := range p.accepted {
		total += count - 1
	}
	return total
}

// slidingPeak finds the most calls in any one second, and how many calls arrived
// with the limit already used up in the second before them.
func slidingPeak(sorted []time.Time, limit int) (peak, over int) {
	start := 0
	for i, at := range sorted {
		for at.Sub(sorted[start]) >= time.Second {
			start++
		}
		inWindow := i - start + 1
		peak = max(peak, inWindow)
		if inWindow > limit {
			over++
		}
	}
	return peak, over
}

func peakIn(sorted []time.Time, window time.Duration) int {
	peak, start := 0, 0
	for i, at := range sorted {
		for at.Sub(sorted[start]) >= window {
			start++
		}
		peak = max(peak, i-start+1)
	}
	return peak
}

func afterLongestGap(sorted []time.Time) (inTenth, inSecond int) {
	longest, from := time.Duration(0), 0
	for i := 1; i < len(sorted); i++ {
		if gap := sorted[i].Sub(sorted[i-1]); gap > longest {
			longest, from = gap, i
		}
	}
	for _, at := range sorted[from:] {
		switch since := at.Sub(sorted[from]); {
		case since < 100*time.Millisecond:
			inTenth++
			inSecond++
		case since < time.Second:
			inSecond++
		}
	}
	return inTenth, inSecond
}

// sendShape shows how evenly calls left: the gaps between them, and how many left together.
func sendShape(sorted []time.Time) (gaps, groups map[string]int) {
	gaps, groups = map[string]int{}, map[string]int{}
	size := 1
	for i := 1; i < len(sorted); i++ {
		gap := sorted[i].Sub(sorted[i-1])
		gaps[gapBucket(gap)]++
		if gap < 2*time.Millisecond {
			size++
			continue
		}
		groups[strconv.Itoa(size)]++
		size = 1
	}
	if len(sorted) > 0 {
		groups[strconv.Itoa(size)]++
	}
	return gaps, groups
}

func gapBucket(gap time.Duration) string {
	switch {
	case gap < time.Millisecond:
		return "under 1 ms"
	case gap < 5*time.Millisecond:
		return "1-5 ms"
	case gap < 10*time.Millisecond:
		return "5-10 ms"
	case gap < 15*time.Millisecond:
		return "10-15 ms"
	case gap < 25*time.Millisecond:
		return "15-25 ms"
	case gap < 100*time.Millisecond:
		return "25-100 ms"
	}
	return "over 100 ms"
}

func busiestSecond(calls []timedCall) *peakWindow {
	if len(calls) == 0 {
		return nil
	}
	byArrival := slices.Clone(calls)
	slices.SortFunc(byArrival, func(a, b timedCall) int { return a.arrived.Compare(b.arrived) })
	bestStart, bestEnd, start := 0, 0, 0
	for i, call := range byArrival {
		for call.arrived.Sub(byArrival[start].arrived) >= time.Second {
			start++
		}
		if i-start > bestEnd-bestStart {
			bestStart, bestEnd = start, i
		}
	}
	window := byArrival[bestStart : bestEnd+1]
	out := &peakWindow{Calls: len(window)}
	first, last := window[0].sent, window[0].sent
	for _, call := range window {
		if call.delayed {
			out.Delayed++
		} else {
			out.Undelayed++
		}
		if call.sent.Before(first) {
			first = call.sent
		}
		if call.sent.After(last) {
			last = call.sent
		}
	}
	out.SentSeconds = last.Sub(first).Seconds()
	if out.SentSeconds > 0 {
		out.SentRate = float64(out.Calls-1) / out.SentSeconds
	}
	return out
}

// sentBetween counts calls that left in a stretch of time.
func (p *provider) sentBetween(channel string, from, to time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	for _, call := range p.timed[channel] {
		if !call.sent.Before(from) && call.sent.Before(to) {
			total++
		}
	}
	return total
}

// middleRate is the rate over the middle 90%, so the start and the last retries do
// not decide what a steady rate was.
func middleRate(sorted []time.Time) float64 {
	if len(sorted) < 20 {
		return 0
	}
	first, last := len(sorted)*5/100, len(sorted)*95/100
	span := sorted[last].Sub(sorted[first]).Seconds()
	if span <= 0 {
		return 0
	}
	return float64(last-first) / span
}

func longestGap(sorted []time.Time) float64 {
	longest := time.Duration(0)
	for i := 1; i < len(sorted); i++ {
		longest = max(longest, sorted[i].Sub(sorted[i-1]))
	}
	return longest.Seconds()
}
