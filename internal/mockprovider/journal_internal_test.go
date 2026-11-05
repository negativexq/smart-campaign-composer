package mockprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAMessageThatCannotBeSavedIsAnsweredAsATemporaryFailure(t *testing.T) {
	t.Parallel()
	provider, err := Open(Behavior{Roll: func() float64 { return 0.9 }}, filepath.Join(t.TempDir(), "journal.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.journal.file.Close(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	provider.Register(mux)
	body, _ := json.Marshal(request{Channel: "sms", To: "+90500", Body: "Hi"})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	request.Header.Set("Idempotency-Key", "k1")
	answer := httptest.NewRecorder()
	mux.ServeHTTP(answer, request)
	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", answer.Code)
	}
	if stats := provider.Stats()["sms"]; stats.Accepted != 0 || stats.ServerErrors != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	if _, known := provider.accepted["k1"]; known {
		t.Fatal("a key that was not saved must not count as remembered")
	}
}

func TestCompactingKeepsEveryKeyTheInboxAndTheReportsOwed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "journal.log")
	owed := errors.New("sender is down")
	behavior := Behavior{
		Roll: func() float64 { return 0.9 }, InboxSize: 1, ReportRetryWait: time.Hour,
		OnEvent: func(Event) error { return owed },
	}
	provider, err := Open(behavior, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"k1", "k2"} {
		if _, _, err := provider.accept(request{Channel: "email", To: "a@example.com", Subject: "S", Body: "B", HTML: strings.Repeat("x", 4096)}, key); err != nil {
			t.Fatal(err)
		}
	}
	before := provider.journal.size
	provider.mu.Lock()
	provider.journal.compactAt = 0
	provider.compactIfLarge()
	after := provider.journal.size
	provider.mu.Unlock()
	if after >= before {
		t.Fatalf("the evicted body should be gone: %d then %d bytes", before, after)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(behavior, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if len(restarted.accepted) != 2 || len(restarted.pending) != 2 {
		t.Fatalf("keys %d, reports %d", len(restarted.accepted), len(restarted.pending))
	}
	if page := restarted.Inbox("", "", 10); len(page.Messages) != 1 || page.Dropped != 1 {
		t.Fatalf("inbox: %+v", page)
	}
}
