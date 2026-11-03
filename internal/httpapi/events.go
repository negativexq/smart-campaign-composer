package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

// SignatureHeader carries the hex HMAC-SHA256 of a webhook body.
const SignatureHeader = "X-Provider-Signature"

// RegisterProviderEvents adds the webhook a provider reports events to. It has no
// session: the signature is what proves the sender.
func RegisterProviderEvents(mux *http.ServeMux, campaigns *campaign.Service, secret string) {
	mux.HandleFunc("POST /api/provider/events", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body is too large"})
			return
		}
		if !validSignature(body, r.Header.Get(SignatureHeader), secret) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "signature does not match"})
			return
		}
		var event struct {
			Event          string    `json:"event"`
			IdempotencyKey string    `json:"idempotency_key"`
			OccurredAt     time.Time `json:"occurred_at"`
		}
		if err := json.Unmarshal(body, &event); err != nil || event.OccurredAt.IsZero() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event is not valid"})
			return
		}
		switch err := campaigns.RecordDeliveryEvent(r.Context(), event.IdempotencyKey, event.Event, event.OccurredAt); {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, campaign.ErrUnknownEvent):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown event"})
		default:
			writeError(w, r, err)
		}
	})
}

func validSignature(body []byte, signature, secret string) bool {
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
