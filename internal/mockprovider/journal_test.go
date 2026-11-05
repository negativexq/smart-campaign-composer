package mockprovider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/mockprovider"
)

// startFrom runs a provider on a journal, as one run of the process does.
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
	t.Cleanup(func() { _ = provider.Close() })
	return provider, delivery.NewHTTPSender(server.URL)
}

func TestARetryAfterARestartIsNotANewMessage(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "journal.log")
	provider, sender := startFrom(t, rolling(0.9), path)
	first, err := sender.Send(context.Background(), sms)
	if err != nil || first.Duplicate {
		t.Fatalf("first: %+v %v", first, err)
	}
	closeProvider(t, provider)

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
	path := filepath.Join(t.TempDir(), "journal.log")
	provider, sender := startFrom(t, rolling(0.25), path)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := sender.Send(ctx, sms); err == nil {
		t.Fatal("the response should have been lost")
	}
	closeProvider(t, provider)

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
	path := filepath.Join(t.TempDir(), "journal.log")
	content := `{"kind":"accepted","key":"kept","id":"mock_a"}` + "\n" + `{"kind":"accepted","key":"cut","id":"mo`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, sender := startFrom(t, rolling(0.9), path)
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
	closeProvider(t, provider)

	_, sender = startFrom(t, rolling(0.9), path)
	for _, message := range []delivery.Message{kept, cut} {
		if result, err := sender.Send(context.Background(), message); err != nil || !result.Duplicate {
			t.Fatalf("%s after another restart: %+v %v", message.IdempotencyKey, result, err)
		}
	}
}

// A crash can only cut off the last line, so anything else is real damage. Starting
// anyway would forget keys and could deliver messages a second time.
func TestAJournalDamagedInTheMiddleStopsTheProviderFromStarting(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "journal.log")
	content := "not json\n" + `{"kind":"accepted","key":"kept","id":"mock_a"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mockprovider.Open(mockprovider.Behavior{}, path); err == nil {
		t.Fatal("it would start with keys missing")
	}
}

func TestAReportUnsentAtARestartIsSentAfterItOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "journal.log")
	down := rolling(0.9)
	down.ReportRetryWait = time.Hour
	attempted := make(chan mockprovider.Event, 1)
	down.OnEvent = func(event mockprovider.Event) error {
		attempted <- event
		return errors.New("sender is down")
	}
	provider, sender := startFrom(t, down, path)
	if _, err := sender.Send(context.Background(), mail); err != nil {
		t.Fatalf("send: %v", err)
	}
	original := nextReport(t, attempted)
	closeProvider(t, provider)

	up := rolling(0.9)
	received := reports(&up)
	restarted, _ := startFrom(t, up, path)
	event := nextReport(t, received)
	if event.Event != "delivered" || event.IdempotencyKey != mail.IdempotencyKey || !event.OccurredAt.Equal(original.OccurredAt) {
		t.Fatalf("event: %+v, original %+v", event, original)
	}
	waitForSettled(t, path, 1)
	closeProvider(t, restarted)

	again := rolling(0.9)
	repeats := reports(&again)
	startFrom(t, again, path)
	select {
	case extra := <-repeats:
		t.Fatalf("a report the sender already has came back: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAReportIsTriedUntilTheSenderIsBack(t *testing.T) {
	t.Parallel()
	behavior := rolling(0.9)
	behavior.ReportRetryWait = time.Millisecond
	received := make(chan mockprovider.Event, 1)
	var calls atomic.Int32
	// More failures than the old limit of five, as when the sender is down for a while.
	behavior.OnEvent = func(event mockprovider.Event) error {
		if calls.Add(1) <= 8 {
			return errors.New("sender is down")
		}
		received <- event
		return nil
	}
	_, sender := start(t, behavior)
	if _, err := sender.Send(context.Background(), mail); err != nil {
		t.Fatalf("send: %v", err)
	}
	if event := nextReport(t, received); event.Event != "delivered" {
		t.Fatalf("event: %+v", event)
	}
}

func TestTheInboxComesBackAfterARestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "journal.log")
	behavior := rolling(0.9)
	received := reports(&behavior)
	provider, sender := startFrom(t, behavior, path)
	for range 2 {
		if _, err := sender.Send(context.Background(), mail); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	if _, err := sender.Send(context.Background(), sms); err != nil {
		t.Fatalf("send: %v", err)
	}
	opened := provider.Inbox("email", "", 1).Messages[0]
	if !provider.Open(opened.ID) {
		t.Fatal("open")
	}
	for range 3 {
		nextReport(t, received)
	}
	closeProvider(t, provider)

	restarted, _ := startFrom(t, rolling(0.9), path)
	page := restarted.Inbox("", "", 10)
	if len(page.Messages) != 2 || page.Unopened != 1 {
		t.Fatalf("page: %+v", page)
	}
	email := restarted.Inbox("email", "", 1).Messages[0]
	if email.ID != opened.ID || email.Body != mail.Body || email.Repeats != 1 || email.OpenedAt == nil {
		t.Fatalf("email: %+v", email)
	}
	closeProvider(t, restarted)

	cleared := rolling(0.9)
	again, _ := startFrom(t, cleared, path)
	again.ClearInbox()
	closeProvider(t, again)
	last, sender := startFrom(t, rolling(0.9), path)
	if got := last.Inbox("", "", 10).Messages; len(got) != 0 {
		t.Fatalf("a cleared inbox came back: %+v", got)
	}
	if result, err := sender.Send(context.Background(), mail); err != nil || !result.Duplicate {
		t.Fatalf("clearing must keep the keys: %+v %v", result, err)
	}
}

func closeProvider(t *testing.T, provider *mockprovider.Server) {
	t.Helper()
	if err := provider.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// waitForSettled waits until the journal shows want finished reports, since a report
// counts as finished just after the sender has it.
func waitForSettled(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(raw), `"kind":"reported"`) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the report was never marked as finished")
}

func TestAProviderThatCannotOpenItsJournalDoesNotStart(t *testing.T) {
	t.Parallel()
	if _, err := mockprovider.Open(mockprovider.Behavior{}, filepath.Join(t.TempDir(), "missing", "journal.log")); err == nil {
		t.Fatal("it would forget everything without saying so")
	}
}
