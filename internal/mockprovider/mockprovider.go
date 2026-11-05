// Package mockprovider imitates an SMS and email gateway, including the ways a
// real one misbehaves. It sends nothing. Like a real gateway it remembers
// idempotency keys, and it measures the rate it receives, so the rate limit can be
// checked from the receiving side.
package mockprovider

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxSMS     = 160
	maxSubject = 80
	window     = time.Second
)

// Behavior sets how often each kind of trouble happens, as fractions of requests.
type Behavior struct {
	RateLimitRate    float64
	ServerErrorRate  float64
	LostResponseRate float64
	// BounceRate is the share of accepted messages that later fail to arrive. It only
	// applies with OnEvent set, since a bounce nobody hears about means nothing.
	BounceRate     float64
	RetryAfterSecs int
	MinDelay       time.Duration
	MaxDelay       time.Duration
	// ReportMinDelay and ReportMaxDelay are how long an arrival report takes.
	ReportMinDelay time.Duration
	ReportMaxDelay time.Duration
	// InboxSize is how many messages the test inbox keeps; the default is 5,000.
	InboxSize int
	// OnEvent receives what the provider reports after accepting a message.
	OnEvent func(Event)
	// Roll overrides the random source in tests.
	Roll func() float64
}

// DefaultBehavior is lively enough to show retries in a demo.
func DefaultBehavior() Behavior {
	return Behavior{
		RateLimitRate:    0.02,
		ServerErrorRate:  0.03,
		LostResponseRate: 0.002,
		BounceRate:       0.03,
		RetryAfterSecs:   1,
		MinDelay:         10 * time.Millisecond,
		MaxDelay:         60 * time.Millisecond,
		ReportMinDelay:   time.Second,
		ReportMaxDelay:   5 * time.Second,
	}
}

// ChannelStats is what one channel received.
type ChannelStats struct {
	Accepted      int `json:"accepted"`
	Duplicates    int `json:"duplicates"`
	Rejected      int `json:"rejected"`
	RateLimited   int `json:"rate_limited"`
	ServerErrors  int `json:"server_errors"`
	PeakPerSecond int `json:"peak_per_second"`
}

// Server is the provider.
type Server struct {
	behavior Behavior
	mu       sync.Mutex
	accepted map[string]string
	keys     *keyLog
	stats    map[string]*ChannelStats
	recent   map[string][]time.Time
	inbox    inbox
}

// New builds a provider.
func New(behavior Behavior) *Server {
	if behavior.Roll == nil {
		behavior.Roll = rand.Float64
	}
	if behavior.InboxSize <= 0 {
		behavior.InboxSize = defaultInboxSize
	}
	return &Server{
		behavior: behavior,
		accepted: map[string]string{},
		stats:    map[string]*ChannelStats{"sms": {}, "email": {}},
		recent:   map[string][]time.Time{},
		inbox:    inbox{size: behavior.InboxSize, byKey: map[string]*InboxMessage{}},
	}
}

// Open builds a provider that remembers its idempotency keys in the file at path, across
// restarts. Without a path they last as long as the process.
func Open(behavior Behavior, path string) (*Server, error) {
	server := New(behavior)
	if path == "" {
		return server, nil
	}
	keys, accepted, err := openKeyLog(path)
	if err != nil {
		return nil, err
	}
	server.keys, server.accepted = keys, accepted
	return server, nil
}

// Register adds the provider's routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/messages", s.send)
	mux.HandleFunc("GET /v1/stats", s.getStats)
	s.registerInbox(mux)
}

type request struct {
	Channel        string `json:"channel"`
	To             string `json:"to"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	HTML           string `json:"html"`
	UnsubscribeURL string `json:"unsubscribe_url"`
}

func (r request) problem() string {
	switch {
	case r.To == "" || r.Body == "":
		return "to and body are required"
	case r.Channel == "sms" && utf8.RuneCountInString(r.Body) > maxSMS:
		return "sms body is too long"
	case r.Channel == "email" && r.Subject == "":
		return "email subject is required"
	case r.Channel == "email" && utf8.RuneCountInString(r.Subject) > maxSubject:
		return "email subject is too long"
	}
	return ""
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var message request
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body is not valid JSON"})
		return
	}
	if message.Channel != "sms" && message.Channel != "email" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown channel"})
		return
	}
	s.arrived(message.Channel)
	key := r.Header.Get("Idempotency-Key")
	problem := message.problem()
	if key == "" {
		problem = "Idempotency-Key header is required"
	}
	if problem != "" {
		s.count(message.Channel, func(stats *ChannelStats) { stats.Rejected++ })
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}

	if !s.pause(r.Context()) {
		return
	}

	roll := s.behavior.Roll()
	switch {
	case roll < s.behavior.RateLimitRate:
		s.count(message.Channel, func(stats *ChannelStats) { stats.RateLimited++ })
		w.Header().Set("Retry-After", strconv.Itoa(s.behavior.RetryAfterSecs))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
		return
	case roll < s.behavior.RateLimitRate+s.behavior.ServerErrorRate:
		s.count(message.Channel, func(stats *ChannelStats) { stats.ServerErrors++ })
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "temporary failure"})
		return
	}

	id, duplicate, err := s.accept(message, key)
	if err != nil {
		// Nothing was accepted, so the sender's retry is the first delivery, not a second.
		slog.Error("remember idempotency key", "error", err)
		s.count(message.Channel, func(stats *ChannelStats) { stats.ServerErrors++ })
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "temporary failure"})
		return
	}
	if roll < s.behavior.RateLimitRate+s.behavior.ServerErrorRate+s.behavior.LostResponseRate {
		// The message got through, but the caller never hears about it.
		<-r.Context().Done()
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"id": id, "duplicate": duplicate})
}

func (s *Server) pause(ctx context.Context) bool {
	delay := s.behavior.MinDelay
	if spread := s.behavior.MaxDelay - s.behavior.MinDelay; spread > 0 {
		delay += rand.N(spread)
	}
	if delay <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

// accept stores the message once, whatever happens to the response. The key is safe
// before the message counts as delivered, so no restart can make a retry look new.
func (s *Server) accept(message request, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := message.Channel
	stats := s.stats[channel]
	if id, known := s.accepted[key]; known {
		stats.Duplicates++
		s.inbox.repeat(key)
		return id, true, nil
	}
	id := "mock_" + cryptorand.Text()
	if s.keys != nil {
		if err := s.keys.add(key, id); err != nil {
			return "", false, err
		}
	}
	s.accepted[key] = id
	stats.Accepted++
	// A bounced message reached nobody, so there is nothing to find in the inbox.
	bounced := s.behavior.OnEvent != nil && s.behavior.Roll() < s.behavior.BounceRate
	if !bounced {
		s.inbox.add(key, id, message)
	}
	s.reportArrival(Event{Event: arrivalEvent(bounced), Channel: channel, MessageID: id, IdempotencyKey: key})

	return id, false, nil
}

// arrived measures the rate as a provider enforces it: every call counts, whatever
// its answer, from the moment it comes in.
func (s *Server) arrived(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	recent := append(s.recent[channel], now)
	first := 0
	for first < len(recent) && now.Sub(recent[first]) >= window {
		first++
	}
	s.recent[channel] = recent[first:]
	stats := s.stats[channel]
	stats.PeakPerSecond = max(stats.PeakPerSecond, len(s.recent[channel]))
}

func arrivalEvent(bounced bool) string {
	if bounced {
		return eventBounced
	}
	return eventDelivered
}

// reportArrival tells the sender what became of a message some time after accepting
// it, as a real gateway does. A lost response does not stop it, because the message
// got through whether or not the caller heard.
func (s *Server) reportArrival(event Event) {
	if s.behavior.OnEvent == nil {
		return
	}
	delay := s.behavior.ReportMinDelay
	if spread := s.behavior.ReportMaxDelay - s.behavior.ReportMinDelay; spread > 0 {
		delay += rand.N(spread)
	}
	time.AfterFunc(delay, func() {
		event.OccurredAt = time.Now().UTC()
		s.behavior.OnEvent(event)
	})
}

func (s *Server) count(channel string, change func(*ChannelStats)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(s.stats[channel])
}

// Stats returns a copy of the counters.
func (s *Server) Stats() map[string]ChannelStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := map[string]ChannelStats{}
	for channel, stats := range s.stats {
		copied[channel] = *stats
	}
	return copied
}

func (s *Server) getStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Stats())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Debug("write response", "error", err)
	}
}
