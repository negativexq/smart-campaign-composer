package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	defaultRetryAfter = time.Second
	// idleConnections keeps a connection for every call a worker makes at once. Go's
	// default of 2 opens a new one for nearly every call, whose delay varies enough
	// to bunch calls up on the way to the provider.
	idleConnections = 64
)

// HTTPSender calls the provider over HTTP.
type HTTPSender struct {
	baseURL string
	client  *http.Client
}

// NewHTTPSender builds a sender for the provider at baseURL.
func NewHTTPSender(baseURL string) *HTTPSender {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = idleConnections
	transport.MaxIdleConnsPerHost = idleConnections
	return &HTTPSender{strings.TrimRight(baseURL, "/"), &http.Client{Transport: transport}}
}

// Send makes one call. A non-2xx response is a result, not an error; an error
// means no response arrived, so the provider may or may not have the message.
func (s *HTTPSender) Send(ctx context.Context, message Message) (Result, error) {
	encoded, err := json.Marshal(map[string]string{
		"channel": message.Channel, "to": message.To, "subject": message.Subject, "body": message.Body,
		"html": message.HTML, "unsubscribe_url": message.UnsubscribeURL,
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
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))

	response, err := s.client.Do(request)
	if err != nil {
		if neverConnected(err) {
			return Result{}, fmt.Errorf("call provider: %w: %w", ErrNotSent, err)
		}
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

// neverConnected is true only when the connection itself failed, before any of the
// message could leave. Anything later may have reached the provider.
func neverConnected(err error) bool {
	var failure *net.OpError
	return errors.As(err, &failure) && failure.Op == "dial"
}

func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds < 0 {
		return defaultRetryAfter
	}
	return time.Duration(seconds) * time.Second
}
