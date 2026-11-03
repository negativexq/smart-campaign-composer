package mockprovider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const (
	eventOpened     = "opened"
	webhookAttempts = 5
	webhookTimeout  = 5 * time.Second
)

// Event is what a provider reports after accepting a message. It carries the
// idempotency key, as real providers carry custom arguments, so a message whose
// response was lost can still be matched.
type Event struct {
	Event          string    `json:"event"`
	Channel        string    `json:"channel"`
	MessageID      string    `json:"message_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// NewWebhook posts events to url, signed with secret. It retries in the background
// with growing waits; an event still unsent after that, or when the provider
// restarts, is lost, as this is a test tool.
func NewWebhook(url, secret string, wait time.Duration) func(Event) {
	client := &http.Client{Timeout: webhookTimeout}
	return func(event Event) {
		body, err := json.Marshal(event)
		if err != nil {
			slog.Error("encode event", "error", err)
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		signature := hex.EncodeToString(mac.Sum(nil))
		go func() {
			delay := wait
			for attempt := 1; attempt <= webhookAttempts; attempt++ {
				if postEvent(client, url, signature, body) {
					return
				}
				if attempt < webhookAttempts {
					time.Sleep(delay)
					delay *= 2
				}
			}
			slog.Warn("event not delivered", "event", event.Event)
		}()
	}
}

// postEvent says whether to stop: a 4xx will not change by repeating it.
func postEvent(client *http.Client, url, signature string, body []byte) bool {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Error("build event request", "error", err)
		return true
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Provider-Signature", signature)
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		slog.Warn("event refused", "status", response.StatusCode)
		return true
	}
	return response.StatusCode < 400
}
