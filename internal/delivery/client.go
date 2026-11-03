package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultRetryAfter = time.Second

// HTTPSender calls the provider over HTTP.
type HTTPSender struct {
	baseURL string
	client  *http.Client
}

// NewHTTPSender builds a sender for the provider at baseURL.
func NewHTTPSender(baseURL string) *HTTPSender {
	return &HTTPSender{strings.TrimRight(baseURL, "/"), &http.Client{}}
}

// Send makes one call. A non-2xx response is a result, not an error; an error
// means no response arrived, so the provider may or may not have the message.
func (s *HTTPSender) Send(ctx context.Context, message Message) (Result, error) {
	encoded, err := json.Marshal(map[string]string{
		"channel": message.Channel, "to": message.To, "subject": message.Subject, "body": message.Body,
	})
	if err != nil {
		return Result{}, fmt.Errorf("encode message: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/messages", bytes.NewReader(encoded))
	if err != nil {
		return Result{}, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", message.IdempotencyKey)

	response, err := s.client.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("call provider: %w", err)
	}
	defer response.Body.Close()

	result := Result{StatusCode: response.StatusCode}
	if response.StatusCode == http.StatusTooManyRequests {
		result.RetryAfter = retryAfter(response.Header.Get("Retry-After"))
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var body struct {
			ID        string `json:"id"`
			Duplicate bool   `json:"duplicate"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			return Result{}, fmt.Errorf("read provider response: %w", err)
		}
		result.MessageID, result.Duplicate = body.ID, body.Duplicate
	}
	return result, nil
}

func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds < 0 {
		return defaultRetryAfter
	}
	return time.Duration(seconds) * time.Second
}
