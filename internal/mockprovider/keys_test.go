package mockprovider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/mockprovider"
)

// startFrom runs a provider on a key file, as one run of the process does.
func startFrom(t *testing.T, behavior mockprovider.Behavior, path string) (*mockprovider.Server, *delivery.HTTPSender) {
	t.Helper()
	provider, err := mockprovider.Open(behavior, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	mux := http.NewServeMux()
	provider.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return provider, delivery.NewHTTPSender(server.URL)
}

func TestARetryAfterARestartIsNotANewMessage(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "keys.log")
	_, sender := startFrom(t, rolling(0.9), path)
	first, err := sender.Send(context.Background(), sms)
	if err != nil || first.Duplicate {
		t.Fatalf("first: %+v %v", first, err)
	}

	restarted, sender := startFrom(t, rolling(0.9), path)
	again, err := sender.Send(context.Background(), sms)
	if err != nil || !again.Duplicate || again.MessageID != first.MessageID {
		t.Fatalf("after a restart: %+v %v, first id %s", again, err, first.MessageID)
	}
	if stats := restarted.Stats()["sms"]; stats.Accepted != 0 || stats.Duplicates != 1 {
		t.Fatalf("stats: %+v", stats)
	}
}

// TestALostResponseAndARestartDeliverOnce is the case that can reach a person twice: the
// message got through, the sender never heard, and the provider restarts before the retry.
func TestALostResponseAndARestartDeliverOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "keys.log")
	_, sender := startFrom(t, rolling(0.25), path)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := sender.Send(ctx, sms); err == nil {
		t.Fatal("the response should have been lost")
	}

	restarted, sender := startFrom(t, rolling(0.9), path)
	retry, err := sender.Send(context.Background(), sms)
	if err != nil || !retry.Duplicate {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if stats := restarted.Stats()["sms"]; stats.Accepted != 0 {
		t.Fatalf("delivered a second time: %+v", stats)
	}
}

func TestALineCutOffByACrashIsDroppedAndTheFileStaysUsable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "keys.log")
	if err := os.WriteFile(path, []byte("mock_a\tkept\nmock_b\tcut"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, sender := startFrom(t, rolling(0.9), path)
	kept := sms
	kept.IdempotencyKey = "kept"
	if result, err := sender.Send(context.Background(), kept); err != nil || !result.Duplicate || result.MessageID != "mock_a" {
		t.Fatalf("kept: %+v %v", result, err)
	}
	cut := sms
	cut.IdempotencyKey = "cut"
	if result, err := sender.Send(context.Background(), cut); err != nil || result.Duplicate {
		t.Fatalf("cut: %+v %v", result, err)
	}

	_, sender = startFrom(t, rolling(0.9), path)
	for _, message := range []delivery.Message{kept, cut} {
		if result, err := sender.Send(context.Background(), message); err != nil || !result.Duplicate {
			t.Fatalf("%s after another restart: %+v %v", message.IdempotencyKey, result, err)
		}
	}
}

func TestAProviderThatCannotKeepItsKeysDoesNotStart(t *testing.T) {
	t.Parallel()
	if _, err := mockprovider.Open(mockprovider.Behavior{}, filepath.Join(t.TempDir(), "missing", "keys.log")); err == nil {
		t.Fatal("it would forget everything without saying so")
	}
}
