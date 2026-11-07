package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
)

func createOn(t *testing.T, api http.Handler, channels []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"brief": exampleBrief, "channels": channels})
	status, response := call(t, api, http.MethodPost, "/api/campaigns", string(body))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, response)
	}
	return response["id"].(string)
}

func TestACampaignOnOneChannelStaysOnThatChannel(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, channel, other string
		present, absent      []string
	}{
		{"sms only", "sms", "email", []string{"sms"}, []string{"email_subject", "email_body"}},
		{"email only", "email", "sms", []string{"email_subject", "email_body"}, []string{"sms"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			api := newAPI(t, fastMock())
			id := createOn(t, api, []string{tt.channel})
			base := "/api/campaigns/" + id

			if status, view := call(t, api, http.MethodPost, base+"/segment", ""); status != http.StatusOK {
				t.Fatalf("segment: %d %v", status, view)
			}
			if got := generate(t, api, id, `{}`); got.Code != http.StatusOK {
				t.Fatalf("generate: %d %s", got.Code, got.Body)
			}
			status, view := call(t, api, http.MethodGet, base, "")
			if status != http.StatusOK {
				t.Fatalf("get: %d", status)
			}
			if got := view["channels"].([]any); len(got) != 1 || got[0] != tt.channel {
				t.Fatalf("channels: %v", got)
			}
			audience := view["audience"].(map[string]any)
			if audience[tt.channel].(float64) == 0 || audience[tt.other].(float64) != 0 {
				t.Fatalf("audience counts only the chosen channel: %v", audience)
			}
			written := view["content"].(map[string]any)
			for _, field := range tt.present {
				if written[field] == "" {
					t.Fatalf("%s is empty: %v", field, written)
				}
			}
			for _, field := range tt.absent {
				if written[field] != "" {
					t.Fatalf("%s must be empty: %v", field, written)
				}
			}

			if status, body := call(t, api, http.MethodPost, base+"/approve", reviewedBody(t, api, base)); status != http.StatusOK {
				t.Fatalf("approve: %d %v", status, body)
			}
			if status, body := call(t, api, http.MethodPost, base+"/dispatch", ""); status != http.StatusAccepted {
				t.Fatalf("dispatch: %d %v", status, body)
			}
			status, progress := call(t, api, http.MethodGet, base+"/progress", "")
			if status != http.StatusOK || progress["total"].(float64) == 0 {
				t.Fatalf("progress: %d %v", status, progress)
			}
			byChannel := progress["by_channel"].(map[string]any)
			if len(byChannel[tt.other].(map[string]any)) != 0 {
				t.Fatalf("a delivery was created on %s: %v", tt.other, byChannel)
			}
			if len(byChannel[tt.channel].(map[string]any)) == 0 {
				t.Fatalf("no delivery on %s: %v", tt.channel, byChannel)
			}
		})
	}
}

func TestSavingEditsCannotAddAnUnusedChannel(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := createOn(t, api, []string{"sms"})
	base := "/api/campaigns/" + id
	call(t, api, http.MethodPost, base+"/segment", "")
	generate(t, api, id, `{}`)

	body := `{"sms":"Hi {{first_name}}, 30% off","email_subject":"Sneaky","email_body":"Sneaky"}`
	if status, response := call(t, api, http.MethodPut, base+"/content", body); status != http.StatusOK {
		t.Fatalf("save: %d %v", status, response)
	}
	_, view := call(t, api, http.MethodGet, base, "")
	if written := view["content"].(map[string]any); written["email_subject"] != "" || written["email_body"] != "" {
		t.Fatalf("content: %v", written)
	}
}

func TestChannelsDefaultToBothAndAreValidated(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	_, view := call(t, api, http.MethodGet, "/api/campaigns/"+createCampaign(t, api, exampleBrief), "")
	if got := view["channels"].([]any); len(got) != 2 {
		t.Fatalf("default: %v", got)
	}
	// The order is the product's, not the caller's.
	_, view = call(t, api, http.MethodGet, "/api/campaigns/"+createOn(t, api, []string{"email", "sms", "email"}), "")
	if got := view["channels"].([]any); len(got) != 2 || got[0] != "sms" || got[1] != "email" {
		t.Fatalf("normalized: %v", got)
	}
	for name, channels := range map[string][]string{"unknown": {"fax"}, "none": {}} {
		body, _ := json.Marshal(map[string]any{"brief": exampleBrief, "channels": channels})
		if status, _ := call(t, api, http.MethodPost, "/api/campaigns", string(body)); status != http.StatusBadRequest {
			t.Errorf("%s: %d", name, status)
		}
	}
}
