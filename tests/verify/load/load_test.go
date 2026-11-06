//go:build verify

// Package load sends a full campaign through the verification stack under provider
// faults, lost Redis data and crashed workers, and checks what must hold every time:
// each message ends in a final state, none is accepted twice, and no channel goes
// over the provider's limit of 100 calls a second. It also records how long each
// run took, so a slowdown shows up next to the earlier results.
//
// Start the stack with `make verify-up`, then run `make verify-load`. A single
// scenario runs with VERIFY_ONLY=name (several: comma separated).
package load

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/tests/verify/stack"
)

const (
	rateLimit  = 100
	runTimeout = 25 * time.Minute
)

// faults are the mock provider's settings for one scenario.
type faults map[string]string

func healthy() faults {
	return faults{"VERIFY_429": "0", "VERIFY_5XX": "0", "VERIFY_LOST": "0", "VERIFY_401": ""}
}

func with(name, value string) faults {
	f := healthy()
	f[name] = value
	return f
}

// run is one scenario in progress. Events record what they did in notes.
type run struct {
	campaign string
	started  time.Time
	mu       sync.Mutex
	notes    map[string]any
	// earlier holds the provider's counters from before each time it stopped. A
	// stopped provider starts again from zero, so its last counters are kept here.
	earlier []map[string]stack.ProviderStats
}

// beforeProviderStops keeps the provider's counters just before it stops. Calls in
// the moment between this read and the stop are not counted.
func (r *run) beforeProviderStops() error {
	stats, err := stack.ProviderErr()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.earlier = append(r.earlier, stats)
	return nil
}

func (r *run) note(name string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes[name] = value
}

func (r *run) at() float64 { return seconds(time.Since(r.started)) }

func seconds(d time.Duration) float64 { return float64(d.Milliseconds()/100) / 10 }

// errFinishedFirst means the campaign finished before the event it was meant to
// face, so the scenario tested nothing.
var errFinishedFirst = fmt.Errorf("the campaign finished before the event")

// sleepUntil waits until the run is the given time old.
func (r *run) sleepUntil(ctx context.Context, offset time.Duration) error {
	select {
	case <-time.After(time.Until(r.started.Add(offset))):
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w at %s", errFinishedFirst, offset)
	}
}

// event breaks something while the campaign is sending. It runs outside the test
// goroutine, so it returns an error instead of failing the test.
type event func(ctx context.Context, r *run) error

type scenario struct {
	name    string
	faults  faults
	event   event
	workers int
	// mayFail allows messages to fail after their last attempt.
	mayFail bool
	// restartsProvider marks a scenario whose provider starts again mid-run, which
	// clears its counters: it can only have accepted as many as were sent, or fewer.
	restartsProvider bool
	// wantEvents are audit events that must appear, as "type channel alert";
	// channel and alert may be empty.
	wantEvents []string
}

func scenarios() []scenario {
	return []scenario{
		{name: "healthy", faults: healthy()},
		{name: "default-faults", faults: faults{"VERIFY_429": "0.02", "VERIFY_5XX": "0.03", "VERIFY_LOST": "0.002", "VERIFY_401": ""}},
		{name: "429-10pct", faults: with("VERIFY_429", "0.10")},
		{name: "5xx-30pct", faults: with("VERIFY_5XX", "0.30"), mayFail: true},
		{name: "lost-5pct", faults: with("VERIFY_LOST", "0.05"), mayFail: true},
		{name: "outage-40s", faults: healthy(), event: outage(20*time.Second, 40*time.Second), restartsProvider: true},
		{name: "worker-crash", faults: healthy(), event: crashWorker(20 * time.Second)},
		{
			name: "5xx70-then-ok", faults: with("VERIFY_5XX", "0.70"), event: fixProviderAt(150 * time.Second), restartsProvider: true,
			wantEvents: []string{"campaign.alert_opened  error_rate", "campaign.alert_closed  error_rate"},
		},
		{
			name: "sms401-2workers", faults: with("VERIFY_401", "sms"), event: fixProviderAt(180 * time.Second), workers: 2, restartsProvider: true,
			wantEvents: []string{"campaign.alert_opened sms credentials_refused", "campaign.alert_closed sms credentials_refused"},
		},
		{name: "redis-lost", faults: healthy(), event: loseRedisAt(30*time.Second, nil)},
		{name: "redis-flushed-10x", faults: healthy(), event: flushRedis(20*time.Second, 10, 2*time.Second)},
		{name: "redis-flushed-10x-2workers", faults: healthy(), event: flushRedis(20*time.Second, 10, 2*time.Second), workers: 2},
		{
			name: "sms401-redis-lost", faults: with("VERIFY_401", "sms"), event: loseRedisAt(60*time.Second, fixProviderAt(150*time.Second)), restartsProvider: true,
			wantEvents: []string{"campaign.alert_opened sms credentials_refused", "campaign.alert_closed sms credentials_refused"},
		},
		{
			name: "both401-paused", faults: with("VERIFY_401", "sms,email"), event: resumeWhenPaused, restartsProvider: true,
			wantEvents: []string{"campaign.paused  ", "campaign.resumed  "},
		},
	}
}

// result is what one scenario measured.
type result struct {
	Name          string                         `json:"name"`
	Workers       int                            `json:"workers"`
	Faults        faults                         `json:"faults"`
	Status        string                         `json:"status"`
	SendSeconds   float64                        `json:"send_seconds"`
	Final         map[string]map[string]int      `json:"final"`
	Outcomes      map[string]map[string]int      `json:"outcomes"`
	AcceptedTwice int                            `json:"accepted_twice"`
	PeakRecorded  map[string]int                 `json:"peak_recorded_per_second"`
	Provider      map[string]stack.ProviderStats `json:"provider"`
	// ProviderEarlier are the provider's counters from before each restart.
	ProviderEarlier []map[string]stack.ProviderStats `json:"provider_before_restarts,omitempty"`
	Events          []string                         `json:"events"`
	Notes           map[string]any                   `json:"notes,omitempty"`
	EventError      string                           `json:"event_error,omitempty"`
	Problems        []string                         `json:"problems,omitempty"`
}

func TestScenarios(t *testing.T) {
	started := time.Now()
	only := strings.Split(os.Getenv("VERIFY_ONLY"), ",")
	var results []result
	for _, s := range scenarios() {
		if only[0] != "" && !slices.Contains(only, s.name) {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			res := runScenario(t, s)
			res.Problems = check(s, res)
			for _, problem := range res.Problems {
				t.Error(problem)
			}
			peak, _ := providerTotals(res)
			t.Logf("%s: %s in %.1f s, peak at the provider %v, accepted twice %d", s.name, res.Status, res.SendSeconds, peak, res.AcceptedTwice)
			results = append(results, res)
		})
	}
	if len(results) > 0 {
		stack.WriteResult(t, stack.Stamp("load", started), results, report(started, results))
	}
}

func runScenario(t *testing.T, s scenario) result {
	workers := max(s.workers, 1)
	// A fresh provider and fresh workers: the provider's counters start at zero.
	stack.Compose(t, s.faults, "up", "-d", "--no-deps", "--force-recreate", "--wait", "--scale", "worker="+strconv.Itoa(workers), "worker", "mock-provider")
	campaign := stack.Approved(t, stack.Brief)

	r := &run{campaign: campaign, started: time.Now(), notes: map[string]any{}}
	stack.Dispatch(t, campaign)
	ctx, cancel := context.WithCancel(context.Background())
	eventDone := make(chan error, 1)
	if s.event != nil {
		go func() { eventDone <- s.event(ctx, r) }()
	} else {
		eventDone <- nil
	}
	status := stack.WaitFinished(t, campaign, runTimeout)
	sent := time.Since(r.started)
	cancel()
	eventErr := <-eventDone

	res := result{
		Name: s.name, Workers: workers, Faults: s.faults, Status: status, SendSeconds: seconds(sent),
		Final:           counts(t, `SELECT channel, status, count(*) FROM campaign_deliveries WHERE campaign_id = '%s' AND status <> 'SKIPPED' GROUP BY 1, 2`, campaign),
		Outcomes:        counts(t, `SELECT channel, outcome, count(*) FROM delivery_attempts WHERE campaign_id = '%s' GROUP BY 1, 2`, campaign),
		Provider:        stack.Provider(t),
		Notes:           r.notes,
		ProviderEarlier: r.earlier,
	}
	if eventErr != nil {
		res.EventError = eventErr.Error()
	}
	res.AcceptedTwice, _ = strconv.Atoi(stack.SQL(t, `SELECT count(*) FROM (SELECT delivery_id FROM delivery_attempts WHERE campaign_id = '%s' AND outcome = 'accepted' GROUP BY 1 HAVING count(*) > 1) x`, campaign))
	res.PeakRecorded = map[string]int{}
	for _, line := range lines(stack.SQL(t, `
		SELECT channel, max(n) FROM (
			SELECT channel, count(*) OVER (PARTITION BY channel ORDER BY occurred_at RANGE BETWEEN interval '999999 microseconds' PRECEDING AND CURRENT ROW) n
			FROM delivery_attempts WHERE campaign_id = '%s') x GROUP BY 1`, campaign)) {
		fields := strings.Split(line, "|")
		res.PeakRecorded[fields[0]], _ = strconv.Atoi(fields[1])
	}
	res.Events = lines(stack.SQL(t, `
		SELECT event_type || ' ' || coalesce(details->>'channel', '') || ' ' || coalesce(details->>'alert', '')
		FROM audit_events WHERE campaign_id = '%s'
		AND event_type IN ('campaign.alert_opened', 'campaign.alert_closed', 'campaign.paused', 'campaign.resumed')
		ORDER BY id`, campaign))
	return res
}

// check lists what went wrong in a run. Each rule holds in every scenario unless
// the scenario says otherwise.
func check(s scenario, res result) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if res.EventError != "" {
		add("the event did not happen as planned: %s", res.EventError)
	}
	if !strings.HasPrefix(res.Status, "COMPLETED") {
		add("the campaign ended %s", res.Status)
	}
	for channel, statuses := range res.Final {
		for status, n := range statuses {
			switch {
			case status == "SENT":
			case status == "FAILED" && s.mayFail:
			default:
				add("%d %s messages ended %s", n, channel, status)
			}
		}
	}
	if res.AcceptedTwice != 0 {
		add("%d messages were accepted twice", res.AcceptedTwice)
	}
	total := 0
	for _, statuses := range res.Final {
		for _, n := range statuses {
			total += n
		}
	}
	if total == 0 {
		add("the campaign had no messages")
	}
	if s.restartsProvider && len(res.ProviderEarlier) == 0 {
		add("the provider was meant to restart but its counters from before were not kept")
	}
	peak, accepted := providerTotals(res)
	for channel, statuses := range res.Final {
		if peak[channel] > rateLimit {
			add("%s reached %d calls in one second at the provider, over %d", channel, peak[channel], rateLimit)
		}
		// Each message the campaign counts as sent was accepted by the provider once.
		// Across a restart, calls between the last read and the stop go uncounted, so
		// the provider may have counted fewer, never more.
		sent := statuses["SENT"]
		if accepted[channel] > sent || (!s.restartsProvider && accepted[channel] != sent) {
			add("the provider accepted %d %s messages, the campaign sent %d", accepted[channel], channel, sent)
		}
	}
	for _, want := range s.wantEvents {
		if !slices.ContainsFunc(res.Events, func(got string) bool { return matches(got, want) }) {
			add("no %q event; events: %v", want, res.Events)
		}
	}
	return problems
}

// providerTotals adds up the provider's counters across its restarts: the highest
// one-second peak and every message it accepted.
func providerTotals(res result) (peak, accepted map[string]int) {
	peak, accepted = map[string]int{}, map[string]int{}
	for _, part := range append(slices.Clone(res.ProviderEarlier), res.Provider) {
		for channel, stats := range part {
			peak[channel] = max(peak[channel], stats.PeakPerSecond)
			accepted[channel] += stats.Accepted
		}
	}
	return peak, accepted
}

// matches compares an event with a wanted one, where an empty channel or alert in
// the wanted event matches any.
func matches(got, want string) bool {
	g, w := strings.Split(got, " "), strings.Split(want, " ")
	for i := range w {
		if w[i] != "" && (i >= len(g) || g[i] != w[i]) {
			return false
		}
	}
	return true
}

func counts(t *testing.T, query string, campaign string) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, line := range lines(stack.SQL(t, query, campaign)) {
		fields := strings.Split(line, "|")
		n, _ := strconv.Atoi(fields[2])
		if out[fields[0]] == nil {
			out[fields[0]] = map[string]int{}
		}
		out[fields[0]][fields[1]] = n
	}
	return out
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// The events.

// fixProviderAt restarts the provider without faults once the run is that old.
func fixProviderAt(offset time.Duration) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		if err := r.beforeProviderStops(); err != nil {
			return err
		}
		r.note("fixed_at", r.at())
		_, err := stack.ComposeErr(healthy(), "up", "-d", "--no-deps", "--force-recreate", "--wait", "mock-provider")
		return err
	}
}

// outage stops the provider for a while.
func outage(offset, length time.Duration) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		if err := r.beforeProviderStops(); err != nil {
			return err
		}
		if _, err := stack.DockerErr("stop", "-t", "0", stack.Container("mock-provider")); err != nil {
			return err
		}
		r.note("down_at", r.at())
		time.Sleep(length)
		_, err := stack.DockerErr("start", stack.Container("mock-provider"))
		r.note("up_at", r.at())
		return err
	}
}

// crashWorker kills the worker until a kill leaves messages mid-send, then starts
// it again: those messages must still end once, and only once.
func crashWorker(offset time.Duration) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		for kill := 1; kill <= 10; kill++ {
			if _, err := stack.DockerErr("kill", stack.Container("worker")); err != nil {
				return err
			}
			orphans, err := stack.DockerErr("exec", stack.Container("postgres"), "psql", "-U", "scc", "-d", "scc", "-At", "-c",
				fmt.Sprintf(`SELECT count(*) FROM campaign_deliveries WHERE campaign_id = '%s' AND status = 'SENDING'`, r.campaign))
			if err != nil {
				return err
			}
			if _, err := stack.DockerErr("start", stack.Container("worker")); err != nil {
				return err
			}
			if orphans != "0" {
				r.note("kills", kill)
				r.note("left_mid_send", orphans)
				return nil
			}
			time.Sleep(4 * time.Second)
		}
		return fmt.Errorf("no kill left a message mid-send")
	}
}

// loseRedisAt kills Redis, starts it empty, then runs next if given.
func loseRedisAt(offset time.Duration, next event) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		redis := stack.Container("redis")
		r.note("redis_killed_at", r.at())
		if _, err := stack.DockerErr("kill", redis); err != nil {
			return err
		}
		time.Sleep(3 * time.Second)
		if _, err := stack.DockerErr("start", redis); err != nil {
			return err
		}
		for range 50 {
			if pong, _ := stack.DockerErr("exec", redis, "redis-cli", "ping"); pong == "PONG" {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if _, err := stack.DockerErr("exec", redis, "redis-cli", "FLUSHALL"); err != nil {
			return err
		}
		r.note("redis_back_at", r.at())
		if next != nil {
			return next(ctx, r)
		}
		return nil
	}
}

// flushRedis empties Redis count times, every interval apart.
func flushRedis(offset time.Duration, count int, every time.Duration) event {
	return func(ctx context.Context, r *run) error {
		var at []float64
		defer func() { r.note("flushed_at", at) }()
		for i := range count {
			if err := r.sleepUntil(ctx, offset+time.Duration(i)*every); err != nil {
				return err
			}
			if _, err := stack.DockerErr("exec", stack.Container("redis"), "redis-cli", "FLUSHALL"); err != nil {
				return err
			}
			at = append(at, r.at())
		}
		return nil
	}
}

// resumeWhenPaused waits for the campaign to pause on refused credentials, fixes the
// provider and resumes it.
func resumeWhenPaused(ctx context.Context, r *run) error {
	client := &http.Client{Timeout: 30 * time.Second}
	call := func(method, path string) (int, string, error) {
		request, err := http.NewRequestWithContext(ctx, method, stack.APIURL()+path, strings.NewReader("{}"))
		if err != nil {
			return 0, "", err
		}
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(stack.Session)
		response, err := client.Do(request)
		if err != nil {
			return 0, "", err
		}
		defer response.Body.Close()
		var body struct{ Status string }
		_ = jsonDecode(response, &body)
		return response.StatusCode, body.Status, nil
	}
	for {
		_, status, err := call(http.MethodGet, "/api/campaigns/"+r.campaign)
		if err != nil {
			return err
		}
		if status == "PAUSED" {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the campaign never paused")
		case <-time.After(2 * time.Second):
		}
	}
	r.note("paused_at", r.at())
	if err := r.beforeProviderStops(); err != nil {
		return err
	}
	if _, err := stack.ComposeErr(healthy(), "up", "-d", "--no-deps", "--force-recreate", "--wait", "mock-provider"); err != nil {
		return err
	}
	code, _, err := call(http.MethodPost, "/api/campaigns/"+r.campaign+"/resume")
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("resume: %d", code)
	}
	r.note("resumed_at", r.at())
	return nil
}

func jsonDecode(response *http.Response, value any) error {
	return json.NewDecoder(response.Body).Decode(value)
}

// report writes the runs as a table, one row per scenario.
func report(started time.Time, results []result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Load verification, %s\n\n", started.UTC().Format("2006-01-02 15:04 UTC"))
	b.WriteString("Each run sends the same campaign through the verification stack. Peak is the most calls the mock provider saw in one second, per channel; the limit is 100.\n\n")
	b.WriteString("| Scenario | Workers | Status | Send time | Sent | Failed | Accepted twice | Peak email / sms | Events | Problems |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		peak, _ := providerTotals(r)
		sent, failed := 0, 0
		for _, statuses := range r.Final {
			sent += statuses["SENT"]
			failed += statuses["FAILED"]
		}
		problems := "none"
		if len(r.Problems) > 0 {
			problems = strings.Join(r.Problems, "; ")
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %.1f s | %d | %d | %d | %d / %d | %s | %s |\n", r.Name, r.Workers, r.Status, r.SendSeconds, sent, failed, r.AcceptedTwice,
			peak["email"], peak["sms"], strings.Join(r.Events, ", "), problems)
	}
	return b.String()
}
