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
	events := reports(&behavior)
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
	event := nextReport(t, events)
	for event.Event != "opened" {
		event = nextReport(t, events)
	}
	if event.IdempotencyKey != mail.IdempotencyKey || event.OccurredAt.IsZero() {
		t.Fatalf("event: %+v", event)
	}
	// The simulated delivery report for the same message may come in meanwhile; only a
	// second open is wrong.
	quiet := time.After(100 * time.Millisecond)
	for waiting := true; waiting; {
		select {
		case extra := <-events:
			if extra.Event == "opened" {
				t.Fatalf("opened twice: %+v", extra)
			}
		case <-quiet:
			waiting = false
		}
	}
	page := provider.Inbox("", "", 10)
	if page.Messages[0].OpenedAt == nil || page.Unopened != 0 {
		t.Fatalf("page: %+v", page)
	}
	if provider.Open("nope") {
		t.Fatal("an unknown message cannot be opened")
	}
}

func TestWebhookSignsTheEventAndAsksForARetryWhileTheSenderIsFailing(t *testing.T) {
	t.Parallel()
	signatures := make(chan string, 2)
	failing := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signatures <- r.Header.Get("X-Provider-Signature")
		if failing {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	notify := mockprovider.NewWebhook(server.URL, "secret")
	event := mockprovider.Event{Event: "opened", IdempotencyKey: "k", OccurredAt: time.Now()}
	if err := notify(event); err == nil {
		t.Fatal("a 503 is worth trying again")
	}
	failing = false
	if err := notify(event); err != nil {
		t.Fatalf("answered: %v", err)
	}
	if first, second := <-signatures, <-signatures; len(first) != 64 || first != second {
		t.Fatalf("signatures: %q %q", first, second)
	}
}

func TestWebhookDoesNotRetryAnAnswerThatWillNotChange(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	if err := mockprovider.NewWebhook(server.URL, "secret")(mockprovider.Event{Event: "opened", OccurredAt: time.Now()}); err != nil {
		t.Fatalf("a 404 must not be retried: %v", err)
	}
}

func TestInboxKeepsTheEmailAsItWasDesigned(t *testing.T) {
	t.Parallel()
	provider, sender := start(t, rolling(0.9))
	designed := mail
	designed.HTML = "<table><tr><td>Hello</td></tr></table>"
	designed.UnsubscribeURL = "https://app.example.com/api/unsubscribe/token"
	if _, err := sender.Send(context.Background(), designed); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := provider.Inbox("", "", 10).Messages[0]
	if got.HTML != designed.HTML || got.UnsubscribeURL != designed.UnsubscribeURL || got.Body != designed.Body {
		t.Fatalf("message: %+v", got)
	}
}

func TestInboxListLeavesTheDesignForWhenAMessageIsOpened(t *testing.T) {
	t.Parallel()
	provider := mockprovider.New(rolling(0.9))
	mux := http.NewServeMux()
	provider.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	designed := mail
	designed.HTML = "<table>designed</table>"
	if _, err := delivery.NewHTTPSender(server.URL).Send(context.Background(), designed); err != nil {
		t.Fatalf("send: %v", err)
	}
	var page mockprovider.InboxPage
	getJSON(t, server.URL+"/v1/inbox", &page)
	if page.Messages[0].HTML != "" {
		t.Fatalf("list: %+v", page.Messages[0])
	}
	var message mockprovider.InboxMessage
	getJSON(t, server.URL+"/v1/inbox/"+page.Messages[0].ID, &message)
	if message.HTML != designed.HTML {
		t.Fatalf("message: %+v", message)
	}
	response, err := http.Get(server.URL + "/v1/inbox/none")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown message: %d", response.StatusCode)
	}
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(into); err != nil {
		t.Fatal(err)
	}
}

// reports collects arrival reports, which come from their own goroutines.
func reports(behavior *mockprovider.Behavior) <-chan mockprovider.Event {
	events := make(chan mockprovider.Event, 8)
	behavior.OnEvent = func(event mockprovider.Event) error {
		events <- event
		return nil
	}
	return events
}

func nextReport(t *testing.T, events <-chan mockprovider.Event) mockprovider.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("no report")
		return mockprovider.Event{}
	}
}

func TestAnAcceptedMessageIsReportedDeliveredOnce(t *testing.T) {
	t.Parallel()
	behavior := rolling(0.9)
	events := reports(&behavior)
	_, sender := start(t, behavior)
	for range 2 {
		if _, err := sender.Send(context.Background(), mail); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	event := nextReport(t, events)
	if event.Event != "delivered" || event.IdempotencyKey != mail.IdempotencyKey || event.OccurredAt.IsZero() {
		t.Fatalf("event: %+v", event)
	}
	select {
	case extra := <-events:
		t.Fatalf("a repeated send was reported again: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestABouncedMessageIsReportedAndNeverReachesTheInbox(t *testing.T) {
	t.Parallel()
	behavior := rolling(0.9)
	behavior.BounceRate = 1
	events := reports(&behavior)
	provider, sender := start(t, behavior)
	if _, err := sender.Send(context.Background(), mail); err != nil {
		t.Fatalf("send: %v", err)
	}
	if event := nextReport(t, events); event.Event != "bounced" || event.IdempotencyKey != mail.IdempotencyKey {
		t.Fatalf("event: %+v", event)
	}
	if got := provider.Inbox("", "", 10).Messages; len(got) != 0 {
		t.Fatalf("inbox: %+v", got)
	}
}

func TestALostResponseIsStillReported(t *testing.T) {
	t.Parallel()
	behavior := rolling(0.25)
	events := reports(&behavior)
	_, sender := start(t, behavior)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := sender.Send(ctx, mail); err == nil {
		t.Fatal("the response should be lost")
	}
	if event := nextReport(t, events); event.Event != "delivered" {
		t.Fatalf("event: %+v", event)
	}
}
