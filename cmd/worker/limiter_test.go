package main

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
)

func settings(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestAnUnknownLimiterIsAnError(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	if _, _, err := chooseLimiter(settings(map[string]string{"RATE_LIMITER": "pacin"}), client, 100, time.Millisecond); err == nil {
		t.Fatal("a misspelt limiter must stop the worker, not choose another")
	}
}

func TestAPaceFasterThanTheLimitIsAnError(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	if _, _, err := chooseLimiter(settings(map[string]string{"RATE_LIMITER": "pacing", "PACING_INTERVAL_MS": "12.5"}), client, 50, time.Millisecond); err == nil {
		t.Fatal("80 a second cannot run under a limit of 50")
	}
	if _, _, err := chooseLimiter(settings(map[string]string{"RATE_LIMITER": "pacing", "PACING_INTERVAL_MS": "nope"}), client, 50, time.Millisecond); err == nil {
		t.Fatal("an interval that is not a number must be an error")
	}
}

func TestOnlySinglePermitsDropTheClaimGap(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	const usual = 20 * time.Millisecond
	for name, want := range map[string]time.Duration{"": 0, "permit": 0, "window": usual, "pacing": usual} {
		limiter, gap, err := chooseLimiter(settings(map[string]string{"RATE_LIMITER": name}), client, 100, usual)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if gap != want {
			t.Errorf("%q: claim gap %v, want %v", name, gap, want)
		}
		if _, single := limiter.(*delivery.RedisPermit); single != (name == "" || name == "permit") {
			t.Errorf("%q built %T", name, limiter)
		}
	}
}
