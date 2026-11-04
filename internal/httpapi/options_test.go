package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
)

const plainBrief = "Remind users in Istanbul who added to their cart in the last 30 days and did not buy"

func createWith(t *testing.T, api http.Handler, options map[string]any) string {
	t.Helper()
	body, _ := json.Marshal(options)
	status, response := call(t, api, http.MethodPost, "/api/campaigns", string(body))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, response)
	}
	return response["id"].(string)
}

func written(t *testing.T, api http.Handler, id string) (map[string]any, map[string]any) {
	t.Helper()
	if status, view := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", ""); status != http.StatusOK {
		t.Fatalf("segment: %d %v", status, view)
	}
	if got := generate(t, api, id, `{}`); got.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", got.Code, got.Body)
	}
	_, view := call(t, api, http.MethodGet, "/api/campaigns/"+id, "")
	return view, view["content"].(map[string]any)
}

func TestAnOfferFromTheFormIsWrittenAndChecked(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	view, messages := written(t, api, createWith(t, api, map[string]any{"brief": plainBrief, "offer_percent": 30}))

	if view["offer_percent"].(float64) != 30 {
		t.Fatalf("view: %v", view["offer_percent"])
	}
	if !strings.Contains(messages["sms"].(string), "30%") || !strings.Contains(messages["email_body"].(string), "30%") {
		t.Fatalf("messages: %v", messages)
	}
	if got := view["content_warnings"].([]any); len(got) != 0 {
		t.Fatalf("the offer is the one asked for: %v", got)
	}

	// Editing it to something else is pointed out, as with an offer from the brief.
	base := "/api/campaigns/" + view["id"].(string)
	edited := `{"sms":"Hi, 50% off","email_subject":"Hello","email_body":"Your 30% discount"}`
	if status, body := call(t, api, http.MethodPut, base+"/content", edited); status != http.StatusOK {
		t.Fatalf("save: %d %v", status, body)
	}
	_, view = call(t, api, http.MethodGet, base, "")
	if got := view["content_warnings"].([]any); len(got) == 0 {
		t.Fatal("a different offer should be pointed out")
	}
}

func TestABriefAndAnOfferThatDisagreeAreRefused(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	conflict, _ := json.Marshal(map[string]any{"brief": "Send a 20% discount to Istanbul", "offer_percent": 30})
	status, body := call(t, api, http.MethodPost, "/api/campaigns", string(conflict))
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "brief") {
		t.Fatalf("%d %v", status, body)
	}

	same := createWith(t, api, map[string]any{"brief": "Send a 30% discount to Istanbul", "offer_percent": 30})
	edit, _ := json.Marshal(map[string]string{"brief": "Send a 20% discount to Istanbul"})
	if status, body := call(t, api, http.MethodPost, "/api/campaigns/"+same+"/segment", string(edit)); status != http.StatusBadRequest {
		t.Fatalf("editing into a conflict: %d %v", status, body)
	}
}

func TestToneChangesTheWriting(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	for tone, want := range map[string]string{"formal": "Sayın ", "urgent": "Acele"} {
		view, messages := written(t, api, createWith(t, api, map[string]any{"brief": plainBrief, "tone": tone}))
		if view["tone"] != tone {
			t.Fatalf("view: %v", view["tone"])
		}
		if !strings.Contains(messages["email_body"].(string), want) {
			t.Fatalf("%s: %v", tone, messages["email_body"])
		}
	}
}

func TestSkippingRecentlyMessagedPeopleIsAVisibleCondition(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := createWith(t, api, map[string]any{"brief": plainBrief, "not_messaged_within_days": 7})
	base := "/api/campaigns/" + id

	hasCondition := func(view map[string]any) bool {
		for _, condition := range view["conditions"].([]any) {
			if condition.(map[string]any)["field"] == "not_messaged_within_days" {
				return condition.(map[string]any)["value"] == "7 days"
			}
		}
		return false
	}
	_, view := call(t, api, http.MethodPost, base+"/segment", "")
	if !hasCondition(view) {
		t.Fatalf("after the first proposal: %v", view["conditions"])
	}
	edit, _ := json.Marshal(map[string]string{"brief": plainBrief + " soon"})
	if _, view = call(t, api, http.MethodPost, base+"/segment", string(edit)); !hasCondition(view) {
		t.Fatalf("after editing the brief: %v", view["conditions"])
	}
}

func TestOptionsAreValidated(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	for name, options := range map[string]map[string]any{
		"unknown tone":  {"tone": "angry"},
		"no discount":   {"offer_percent": 0},
		"too much":      {"offer_percent": 101},
		"negative skip": {"not_messaged_within_days": -1},
		"long skip":     {"not_messaged_within_days": 366},
	} {
		options["brief"] = plainBrief
		body, _ := json.Marshal(options)
		if status, response := call(t, api, http.MethodPost, "/api/campaigns", string(body)); status != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, status, response)
		}
	}
}
