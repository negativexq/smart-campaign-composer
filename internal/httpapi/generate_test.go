package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/content"
)

type sseEvent struct {
	name string
	data map[string]any
}

func readEvents(t *testing.T, body string) []sseEvent {
	t.Helper()
	events := []sseEvent{}
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		name, data, ok := strings.Cut(block, "\ndata: ")
		if !ok || !strings.HasPrefix(name, "event: ") {
			t.Fatalf("malformed event %q", block)
		}
		decoded := map[string]any{}
		if err := json.Unmarshal([]byte(data), &decoded); err != nil {
			t.Fatalf("event data %q: %v", data, err)
		}
		events = append(events, sseEvent{strings.TrimPrefix(name, "event: "), decoded})
	}
	return events
}

func generate(t *testing.T, api http.Handler, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/campaigns/"+id+"/generate", strings.NewReader(body)))
	return recorder
}

func campaignWithAudience(t *testing.T, api http.Handler) string {
	t.Helper()
	id := createCampaign(t, api, exampleBrief)
	if status, _ := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", ""); status != http.StatusOK {
		t.Fatalf("segment: %d", status)
	}
	return id
}

func fastMock() *ai.Mock {
	mock := ai.NewMock()
	mock.StreamDelay = 0
	return mock
}

func TestGenerateStreamsToAValidatedResult(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := campaignWithAudience(t, api)

	response := generate(t, api, id, "")
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("response: %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	events := readEvents(t, response.Body.String())
	if events[0].name != "started" || events[len(events)-1].name != "done" {
		t.Fatalf("events: %+v", events)
	}

	streamed := map[string]string{}
	for _, event := range events {
		if event.name == "delta" {
			streamed[event.data["field"].(string)] += event.data["text"].(string)
		}
	}
	final := events[len(events)-1].data
	if final["status"] != "READY_FOR_REVIEW" {
		t.Fatalf("final: %+v", final)
	}
	stored := final["content"].(map[string]any)
	for _, field := range []string{content.FieldSMS, content.FieldEmailSubject, content.FieldEmailBody} {
		if stored[field] == "" || streamed[field] != stored[field] {
			t.Errorf("%s: streamed %q, stored %q", field, streamed[field], stored[field])
		}
	}
}

func TestGeneratePreflightFailuresAreOrdinaryResponses(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())

	if got := generate(t, api, "00000000-0000-0000-0000-000000000000", ""); got.Code != http.StatusNotFound {
		t.Errorf("unknown campaign: %d", got.Code)
	}
	id := createCampaign(t, api, exampleBrief)
	if got := generate(t, api, id, ""); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "no audience") {
		t.Errorf("no audience: %d %s", got.Code, got.Body)
	}
	id = campaignWithAudience(t, api)
	long := `{"instruction":"` + strings.Repeat("x", 501) + `"}`
	if got := generate(t, api, id, long); got.Code != http.StatusBadRequest {
		t.Errorf("long instruction: %d", got.Code)
	}
	if got := generate(t, api, id, `{"sql":"x"}`); got.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", got.Code)
	}
}

type outageWhileWriting struct {
	*ai.Mock
}

func (outageWhileWriting) StreamContent(_ context.Context, _ ai.ContentRequest, handler ai.StreamHandler) (ai.Response, error) {
	handler.OnText(`{"sms":"partial`)
	return ai.Response{}, ai.ErrUnavailable
}

func TestAFailureMidStreamArrivesAsAnEventAndCanBeRetried(t *testing.T) {
	t.Parallel()
	api := newAPI(t, outageWhileWriting{fastMock()})
	id := campaignWithAudience(t, api)

	events := readEvents(t, generate(t, api, id, "").Body.String())
	last := events[len(events)-1]
	if last.name != "error" || !strings.Contains(last.data["error"].(string), "unavailable") {
		t.Fatalf("last: %+v", last)
	}
	_, view := call(t, api, http.MethodGet, "/api/campaigns/"+id, "")
	if view["status"] != "GENERATION_FAILED" || view["content"] != nil {
		t.Fatalf("view: %+v", view)
	}
	if got := generate(t, api, id, ""); got.Code != http.StatusOK {
		t.Fatalf("retry must be allowed: %d", got.Code)
	}
}

func TestSavingEditedContent(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := campaignWithAudience(t, api)
	generate(t, api, id, "")

	path := "/api/campaigns/" + id + "/content"
	status, view := call(t, api, http.MethodPut, path, `{"sms":"Hello {{first_name}}","email_subject":"Hi","email_body":"Body"}`)
	stored, _ := view["content"].(map[string]any)
	if status != http.StatusOK || stored["sms"] != "Hello {{first_name}}" {
		t.Fatalf("save: %d %+v", status, view)
	}

	status, body := call(t, api, http.MethodPut, path, `{"sms":"Use {{coupon}}","email_subject":"Hi","email_body":"Body"}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid: %d", status)
	}
	issues := body["issues"].([]any)
	if issues[0].(map[string]any)["field"] != "sms" {
		t.Fatalf("issues: %+v", issues)
	}
}

func TestSampleRecipients(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := createCampaign(t, api, exampleBrief)
	path := "/api/campaigns/" + id + "/sample-recipients"
	if status, _ := call(t, api, http.MethodGet, path, ""); status != http.StatusConflict {
		t.Fatalf("no audience: %d", status)
	}
	call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", "")
	status, body := call(t, api, http.MethodGet, path, "")
	if status != http.StatusOK || len(body["recipients"].([]any)) != 3 {
		t.Fatalf("got %d %+v", status, body)
	}
}

func TestApproveAndDispatch(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := campaignWithAudience(t, api)
	base := "/api/campaigns/" + id

	if status, _ := call(t, api, http.MethodPost, base+"/approve", ""); status != http.StatusConflict {
		t.Fatalf("approve without content: %d", status)
	}
	generate(t, api, id, "")
	if status, _ := call(t, api, http.MethodPost, base+"/dispatch", ""); status != http.StatusConflict {
		t.Fatalf("dispatch before approval: %d", status)
	}
	status, view := call(t, api, http.MethodPost, base+"/approve", "")
	if status != http.StatusOK || view["status"] != "APPROVED" || view["approved_at"] == nil {
		t.Fatalf("approve: %d %+v", status, view)
	}
	for range 2 {
		if status, _ := call(t, api, http.MethodPost, base+"/dispatch", ""); status != http.StatusAccepted {
			t.Fatalf("dispatch: %d", status)
		}
	}

	status, progress := call(t, api, http.MethodGet, base+"/progress", "")
	if status != http.StatusOK || progress["total"] != float64(18) {
		t.Fatalf("progress: %d %+v", status, progress)
	}
	status, list := call(t, api, http.MethodGet, base+"/deliveries?channel=sms&status=QUEUED", "")
	rows := list["deliveries"].([]any)
	if status != http.StatusOK || len(rows) == 0 {
		t.Fatalf("deliveries: %d %+v", status, list)
	}
	row := rows[0].(map[string]any)
	if row["channel"] != "sms" || row["email"] != nil || row["phone"] != nil {
		t.Fatalf("row: %+v", row)
	}
	if status, _ := call(t, api, http.MethodGet, base+"/deliveries?status=NOPE", ""); status != http.StatusBadRequest {
		t.Fatalf("bad filter: %d", status)
	}
	status, attempts := call(t, api, http.MethodGet, base+"/deliveries/"+row["id"].(string)+"/attempts", "")
	if status != http.StatusOK || len(attempts["attempts"].([]any)) != 0 {
		t.Fatalf("attempts: %d %+v", status, attempts)
	}
	if status, _ := call(t, api, http.MethodGet, base+"/deliveries/00000000-0000-0000-0000-000000000000/attempts", ""); status != http.StatusNotFound {
		t.Fatalf("unknown delivery: %d", status)
	}
}
