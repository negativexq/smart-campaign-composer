package delivery_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
)

func TestAClosedPortMeansTheMessageWasNotSent(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	_, err := delivery.NewHTTPSender(url).Send(context.Background(), delivery.Message{Channel: "sms"})
	if !errors.Is(err, delivery.ErrNotSent) {
		t.Fatalf("err = %v", err)
	}
}

func TestALostResponseIsNotCalledNotSent(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()

	_, err := delivery.NewHTTPSender(server.URL).Send(context.Background(), delivery.Message{Channel: "sms"})
	if err == nil || errors.Is(err, delivery.ErrNotSent) {
		t.Fatalf("the provider may have the message: %v", err)
	}
}
