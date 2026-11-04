package mockprovider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/mockprovider"
)

func start(t *testing.T, behavior mockprovider.Behavior) (*mockprovider.Server, *delivery.HTTPSender) {
	t.Helper()
	provider := mockprovider.New(behavior)
	mux := http.NewServeMux()
	provider.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return provider, delivery.NewHTTPSender(server.URL)
}

func rolling(value float64) mockprovider.Behavior {
	return mockprovider.Behavior{RateLimitRate: 0.1, ServerErrorRate: 0.1, LostResponseRate: 0.1, RetryAfterSecs: 7, Roll: func() float64 { return value }}
}

var sms = delivery.Message{Channel: "sms", To: "+90500", Body: "Hi", IdempotencyKey: "k1"}

func TestAcceptsAndRemembersIdempotencyKeys(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.9))

	first, err := sender.Send(context.Background(), sms)
	if err != nil || first.StatusCode != http.StatusAccepted || first.MessageID == "" || first.Duplicate {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := sender.Send(context.Background(), sms)
	if err != nil || second.MessageID != first.MessageID || !second.Duplicate {
		t.Fatalf("second: %+v %v", second, err)
	}
	if stats := provider.Stats()["sms"]; stats.Accepted != 1 || stats.Duplicates != 1 {
		t.Fatalf("stats: %+v", stats)
	}
}

func TestRateLimitsWithRetryAfter(t *testing.T) {
	t.Parallel()
	_, sender := start(t, rolling(0.05))
	result, err := sender.Send(context.Background(), sms)
	if err != nil || result.StatusCode != http.StatusTooManyRequests || result.RetryAfter != 7*time.Second {
		t.Fatalf("got %+v %v", result, err)
	}
}

func TestServerErrorsAcceptNothing(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.15))
	result, err := sender.Send(context.Background(), sms)
	if err != nil || result.StatusCode != http.StatusInternalServerError {
		t.Fatalf("got %+v %v", result, err)
	}
	if provider.Stats()["sms"].Accepted != 0 {
		t.Fatal("a 500 must not store the message")
	}
}

func TestALostResponseStillAcceptsTheMessage(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.25))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := sender.Send(ctx, sms); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if provider.Stats()["sms"].Accepted != 1 {
		t.Fatal("this is the case that makes an outcome unknown")
	}
}

func TestRejectsWhatARealGatewayWould(t *testing.T) {
	t.Parallel()
	_, sender := start(t, rolling(0.9))
	for name, message := range map[string]delivery.Message{
		"no key":       {Channel: "sms", To: "x", Body: "y"},
		"no recipient": {Channel: "sms", Body: "y", IdempotencyKey: "a"},
		"long sms":     {Channel: "sms", To: "x", Body: strings.Repeat("y", 161), IdempotencyKey: "b"},
		"no subject":   {Channel: "email", To: "x", Body: "y", IdempotencyKey: "c"},
		"long subject": {Channel: "email", To: "x", Body: "y", Subject: strings.Repeat("s", 81), IdempotencyKey: "d"},
	} {
		result, err := sender.Send(context.Background(), message)
		if err != nil || result.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %+v %v", name, result, err)
		}
	}
}

func TestMeasuresThePeakRate(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.9))
	for i := range 5 {
		message := sms
		message.IdempotencyKey = string(rune('a' + i))
		if _, err := sender.Send(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if got := provider.Stats()["sms"].PeakPerSecond; got != 5 {
		t.Fatalf("peak: %d", got)
	}
}

func TestThePeakRateCountsCallsThatWereNotAccepted(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0))
	for i := range 3 {
		message := sms
		message.IdempotencyKey = string(rune('a' + i))
		if _, err := sender.Send(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	stats := provider.Stats()["sms"]
	if stats.Accepted != 0 || stats.PeakPerSecond != 3 {
		t.Fatalf("stats: %+v", stats)
	}
}
