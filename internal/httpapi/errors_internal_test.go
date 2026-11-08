package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
)

// Each reason is a sentence, so the message read "does not.." and ".; " before.
func TestRejectedMessagesReadAsSentences(t *testing.T) {
	t.Parallel()
	rejected := fmt.Errorf("%w: %w", campaign.ErrContentRejected, &content.ValidationError{Issues: []content.Issue{
		{Field: content.FieldSMS, Reason: "unknown_variable", Message: "SMS uses {{discount_code}}, but this campaign has no discount code."},
		{Field: content.FieldSMS, Reason: "invented_discount", Message: "SMS mentions a 15% discount that the brief does not."},
	}})
	recorder := httptest.NewRecorder()
	writeError(recorder, httptest.NewRequest(http.MethodPost, "/", nil), rejected)
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusUnprocessableEntity || strings.Contains(body.Error, "..") || strings.Contains(body.Error, ".;") {
		t.Fatalf("%d %q", recorder.Code, body.Error)
	}
}

// A bad code was answered with the message for tone, discount and days.
func TestABadCodeIsNamed(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	writeError(recorder, httptest.NewRequest(http.MethodPost, "/", nil), campaign.ErrInvalidCode)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "discount code of 3 to 30") {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
}
