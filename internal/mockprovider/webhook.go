package mockprovider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	eventOpened    = "opened"
	eventDelivered = "delivered"
	eventBounced   = "bounced"
	webhookTimeout = 5 * time.Second
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

// NewWebhook posts each event to url once, signed with secret. An error means it is
// worth trying again; a refusal is not, since repeating it would not change the answer.
func NewWebhook(url, secret string) func(Event) error {
	client := &http.Client{Timeout: webhookTimeout}
	return func(event Event) error {
		body, err := json.Marshal(event)
		if err != nil {
			slog.Error("encode event", "error", err)
			return nil
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		return postEvent(client, url, hex.EncodeToString(mac.Sum(nil)), body)
	}
}

func postEvent(client *http.Client, url, signature string, body []byte) error {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Error("build event request", "error", err)
		return nil
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Provider-Signature", signature)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("post event: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		slog.Warn("event refused", "status", response.StatusCode)
		return nil
	}
	if response.StatusCode >= 500 {
		return fmt.Errorf("post event: status %d", response.StatusCode)
	}
	return nil
}
