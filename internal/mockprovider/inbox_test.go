package mockprovider_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/mockprovider"
)

var mail = delivery.Message{Channel: "email", To: "ayse@example.com", Subject: "Your cart", Body: "Hello Ayşe", IdempotencyKey: "m1"}

func TestInboxShowsAcceptedMessagesAsTheyWereReceived(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.9))
	for _, message := range []delivery.Message{sms, mail} {
		if _, err := sender.Send(context.Background(), message); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	page := provider.Inbox("", "", 10)
	if page.SMS != 1 || page.Email != 1 || page.Matching != 2 || len(page.Messages) != 2 {
		t.Fatalf("page: %+v", page)
	}
	newest := page.Messages[0]
	if newest.Channel != "email" || newest.To != mail.To || newest.Subject != mail.Subject || newest.Body != mail.Body || newest.ID == "" {
		t.Fatalf("newest first, whole message: %+v", newest)
	}
}

func TestInboxHasOneEntryWhenAKeyIsRepeated(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.9))
	for range 3 {
		if _, err := sender.Send(context.Background(), sms); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	page := provider.Inbox("", "", 10)
	if len(page.Messages) != 1 || page.Messages[0].Repeats != 2 {
		t.Fatalf("page: %+v", page)
	}
}

func TestInboxOnlyHoldsWhatTheProviderAccepted(t *testing.T) {
	t.Parallel()
	for name, roll := range map[string]float64{"rate limited": 0.05, "server error": 0.15} {
		provider, sender := start(t, rolling(roll))
		if _, err := sender.Send(context.Background(), sms); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := provider.Inbox("", "", 10); got.Matching != 0 {
			t.Fatalf("%s reached the inbox: %+v", name, got)
		}
	}
}

func TestInboxShowsAMessageWhoseResponseWasLost(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.25))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := sender.Send(ctx, sms); err == nil {
		t.Fatal("expected no response")
	}
	if got := provider.Inbox("", "", 10); got.Matching != 1 {
		t.Fatalf("this message did arrive, which is why its outcome is unknown: %+v", got)
	}
}

func TestInboxFiltersAndCounts(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.9))
	other := delivery.Message{Channel: "sms", To: "+90555", Body: "Discount inside", IdempotencyKey: "k2"}
	for _, message := range []delivery.Message{sms, mail, other} {
		if _, err := sender.Send(context.Background(), message); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	tests := []struct {
		name, channel, search string
		limit, matching, rows int
	}{
		{"channel", "sms", "", 10, 2, 2},
		{"recipient", "", "ayse@", 10, 1, 1},
		{"subject", "", "CART", 10, 1, 1},
		{"body", "", "discount", 10, 1, 1},
		{"channel and text", "email", "discount", 10, 0, 0},
		{"limit keeps the true match count", "", "", 2, 3, 2},
	}
	for _, tt := range tests {
		page := provider.Inbox(tt.channel, tt.search, tt.limit)
		if page.Matching != tt.matching || len(page.Messages) != tt.rows || page.SMS != 2 || page.Email != 1 {
			t.Errorf("%s: %+v", tt.name, page)
		}
	}
}

func TestInboxDropsTheOldestWhenFullAndSaysSo(t *testing.T) {
	t.Parallel()
	behavior := rolling(0.9)
	behavior.InboxSize = 2
	provider, sender := start(t, behavior)
	for _, key := range []string{"a", "b", "c"} {
		if _, err := sender.Send(context.Background(), delivery.Message{Channel: "sms", To: key, Body: "Hi", IdempotencyKey: key}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	page := provider.Inbox("", "", 10)
	if len(page.Messages) != 2 || page.Messages[0].To != "c" || page.Messages[1].To != "b" || page.Dropped != 1 {
		t.Fatalf("page: %+v", page)
	}
	// A dropped message's key is still known, so a repeat stays harmless.
	result, err := sender.Send(context.Background(), delivery.Message{Channel: "sms", To: "a", Body: "Hi", IdempotencyKey: "a"})
	if err != nil || !result.Duplicate {
		t.Fatalf("repeat: %+v %v", result, err)
	}
}

func TestClearingTheInboxKeepsIdempotencyAndStats(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.9))
	if _, err := sender.Send(context.Background(), sms); err != nil {
		t.Fatalf("send: %v", err)
	}
	provider.ClearInbox()
	if got := provider.Inbox("", "", 10); got.Matching != 0 || got.SMS != 0 {
		t.Fatalf("not cleared: %+v", got)
	}
	result, err := sender.Send(context.Background(), sms)
	if err != nil || !result.Duplicate {
		t.Fatalf("a cleared inbox must not allow a second send: %+v %v", result, err)
	}
	if got := provider.Inbox("", "", 10); got.Matching != 0 {
		t.Fatalf("a repeat must not reappear: %+v", got)
	}
	if provider.Stats()["sms"].Accepted != 1 {
		t.Fatalf("stats: %+v", provider.Stats())
	}
}

func TestInboxOverHTTP(t *testing.T) {
	t.Parallel()
	provider := mockprovider.New(rolling(0.9))
	mux := http.NewServeMux()
	provider.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if _, err := delivery.NewHTTPSender(server.URL).Send(context.Background(), mail); err != nil {
		t.Fatalf("send: %v", err)
	}

	page, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.HasPrefix(page.Header.Get("Content-Type"), "text/html") || page.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("page: %d %v", page.StatusCode, page.Header)
	}

	list, err := http.Get(server.URL + "/v1/inbox?channel=email&q=ayse")
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	var body mockprovider.InboxPage
	if err := json.NewDecoder(list.Body).Decode(&body); err != nil || body.Matching != 1 || body.Messages[0].Subject != mail.Subject {
		t.Fatalf("list: %+v %v", body, err)
	}

	bad, err := http.Get(server.URL + "/v1/inbox?channel=fax")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad channel: %d", bad.StatusCode)
	}

	request, _ := http.NewRequest(http.MethodDelete, server.URL+"/v1/inbox", nil)
	cleared, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer cleared.Body.Close()
	if cleared.StatusCode != http.StatusNoContent || provider.Inbox("", "", 10).Matching != 0 {
		t.Fatalf("clear: %d", cleared.StatusCode)
	}
}

func TestOpeningAMessageReportsOnceWithItsKey(t *testing.T) {
	t.Parallel()
	behavior := rolling(0.9)
	var events []mockprovider.Event
	behavior.OnEvent = func(event mockprovider.Event) { events = append(events, event) }
	provider, sender := start(t, behavior)
	if _, err := sender.Send(context.Background(), mail); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := provider.Inbox("", "", 10).Unopened; got != 1 {
		t.Fatalf("unopened: %d", got)
	}
	id := provider.Inbox("", "", 10).Messages[0].ID
	for range 2 {
		if !provider.Open(id) {
			t.Fatal("open")
		}
	}
	if len(events) != 1 || events[0].Event != "opened" || events[0].IdempotencyKey != mail.IdempotencyKey || events[0].OccurredAt.IsZero() {
		t.Fatalf("events: %+v", events)
	}
	page := provider.Inbox("", "", 10)
	if page.Messages[0].OpenedAt == nil || page.Unopened != 0 {
		t.Fatalf("page: %+v", page)
	}
	if provider.Open("nope") {
		t.Fatal("an unknown message cannot be opened")
	}
}

func TestWebhookSignsTheEventAndRetriesUntilAnswered(t *testing.T) {
	t.Parallel()
	calls := make(chan string, 3)
	failures := 2
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls <- r.Header.Get("X-Provider-Signature")
		if failures > 0 {
			failures--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	notify := mockprovider.NewWebhook(server.URL, "secret", time.Millisecond)
	notify(mockprovider.Event{Event: "opened", IdempotencyKey: "k", OccurredAt: time.Now()})
	for range 3 {
		select {
		case signature := <-calls:
			if len(signature) != 64 {
				t.Fatalf("signature: %q", signature)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the webhook stopped retrying")
		}
	}
}

func TestWebhookDoesNotRetryAnswerThatWillNotChange(t *testing.T) {
	t.Parallel()
	calls := make(chan struct{}, 5)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls <- struct{}{}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	mockprovider.NewWebhook(server.URL, "secret", time.Millisecond)(mockprovider.Event{Event: "opened", OccurredAt: time.Now()})
	<-calls
	select {
	case <-calls:
		t.Fatal("a 404 must not be retried")
	case <-time.After(100 * time.Millisecond):
	}
}
