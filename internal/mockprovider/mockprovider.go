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
	RetryAfterSecs   int
	MinDelay         time.Duration
	MaxDelay         time.Duration
	// Roll overrides the random source in tests.
	Roll func() float64
}

// DefaultBehavior is lively enough to show retries in a demo.
func DefaultBehavior() Behavior {
	return Behavior{
		RateLimitRate:    0.02,
		ServerErrorRate:  0.03,
		LostResponseRate: 0.002,
		RetryAfterSecs:   1,
		MinDelay:         10 * time.Millisecond,
		MaxDelay:         60 * time.Millisecond,
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
	stats    map[string]*ChannelStats
	recent   map[string][]time.Time
}

// New builds a provider.
func New(behavior Behavior) *Server {
	if behavior.Roll == nil {
		behavior.Roll = rand.Float64
	}
	return &Server{
		behavior: behavior,
		accepted: map[string]string{},
		stats:    map[string]*ChannelStats{"sms": {}, "email": {}},
		recent:   map[string][]time.Time{},
	}
}

// Register adds the provider's routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/messages", s.send)
	mux.HandleFunc("GET /v1/stats", s.getStats)
}

type request struct {
	Channel string `json:"channel"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
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

	id, duplicate := s.accept(message.Channel, key)
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

// accept stores the message once, whatever happens to the response.
func (s *Server) accept(channel, key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.stats[channel]
	if id, known := s.accepted[key]; known {
		stats.Duplicates++
		return id, true
	}
	id := "mock_" + cryptorand.Text()
	s.accepted[key] = id
	stats.Accepted++

	now := time.Now()
	recent := append(s.recent[channel], now)
	first := 0
	for first < len(recent) && now.Sub(recent[first]) >= window {
		first++
	}
	s.recent[channel] = recent[first:]
	stats.PeakPerSecond = max(stats.PeakPerSecond, len(s.recent[channel]))
	return id, false
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
