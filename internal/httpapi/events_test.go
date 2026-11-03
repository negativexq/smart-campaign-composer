package httpapi_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/httpapi"
)

func TestProviderEventsNeedAValidSignature(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	httpapi.RegisterProviderEvents(mux, nil, "secret")

	body := `{"event":"opened","idempotency_key":"k","occurred_at":"2026-11-03T10:00:00Z"}`
	for name, signature := range map[string]string{
		"missing":    "",
		"not hex":    "nope",
		"wrong key":  sign(body, "other"),
		"other body": sign(body+" ", "secret"),
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/provider/events", strings.NewReader(body))
		request.Header.Set(httpapi.SignatureHeader, signature)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, response.Code)
		}
	}
}

func sign(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}
