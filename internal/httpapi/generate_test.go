package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	api.ServeHTTP(recorder, signIn(httptest.NewRequest(http.MethodPost, "/api/campaigns/"+id+"/generate", strings.NewReader(body))))
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
	if status != http.StatusOK || len(body["recipients"].([]any)) < 3 {
		t.Fatalf("got %d %+v", status, body)
	}
}

func TestProposalShowsOnlyWordsTheMarketerWrote(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := createCampaign(t, api, exampleBrief)
	_, view := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", "")
	evidence := view["proposal"].(map[string]any)["evidence"].([]any)
	if len(evidence) == 0 {
		t.Fatalf("no evidence: %+v", view["proposal"])
	}
	for _, entry := range evidence {
		if quote := entry.(map[string]any)["quote"].(string); !strings.Contains(exampleBrief, quote) {
			t.Fatalf("%q is not in the brief", quote)
		}
	}
}

func TestAudienceBreakdown(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := createCampaign(t, api, exampleBrief)
	path := "/api/campaigns/" + id + "/audience/breakdown"
	if status, _ := call(t, api, http.MethodGet, path, ""); status != http.StatusConflict {
		t.Fatalf("no audience: %d", status)
	}
	_, view := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", "")
	status, body := call(t, api, http.MethodGet, path, "")
	if status != http.StatusOK {
		t.Fatalf("got %d %+v", status, body)
	}
	steps := body["steps"].([]any)
	if len(steps) != len(view["conditions"].([]any)) {
		t.Fatalf("steps %+v for conditions %+v", steps, view["conditions"])
	}
	last := steps[len(steps)-1].(map[string]any)["remaining"]
	if last != view["audience"].(map[string]any)["total"] {
		t.Fatalf("last step %v does not match the audience %+v", last, view["audience"])
	}
}

func TestApproveAndDispatch(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := campaignWithAudience(t, api)
	base := "/api/campaigns/" + id

	if status, _ := call(t, api, http.MethodPost, base+"/approve", reviewedBody(t, api, base)); status != http.StatusConflict {
		t.Fatalf("approve without content: %d", status)
	}
	generate(t, api, id, "")
	if status, _ := call(t, api, http.MethodPost, base+"/dispatch", ""); status != http.StatusConflict {
		t.Fatalf("dispatch before approval: %d", status)
	}
	// Issue #9: a request that does not name the review it was given on is refused.
	for _, body := range []string{"", "{}", `{"revision":""}`} {
		if status, _ := call(t, api, http.MethodPost, base+"/approve", body); status != http.StatusBadRequest {
			t.Fatalf("approve with %q: %d", body, status)
		}
	}
	if status, _ := call(t, api, http.MethodPost, base+"/approve", `{"revision":"0000000000000000"}`); status != http.StatusConflict {
		t.Fatalf("approve with a revision never shown: %d", status)
	}
	status, view := call(t, api, http.MethodPost, base+"/approve", reviewedBody(t, api, base))
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

func TestScheduling(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := campaignWithAudience(t, api)
	generate(t, api, id, "")
	call(t, api, http.MethodPost, "/api/campaigns/"+id+"/approve", reviewedBody(t, api, "/api/campaigns/"+id))
	path := "/api/campaigns/" + id + "/schedule"

	status, body := call(t, api, http.MethodPost, path, `{"kind":"weekly","days":[],"time":"09:00","timezone":"Europe/Istanbul"}`)
	if status != http.StatusBadRequest || !strings.Contains(body["error"].(string), "day") {
		t.Fatalf("invalid: %d %+v", status, body)
	}
	status, view := call(t, api, http.MethodPost, path, `{"kind":"weekly","days":["mon","wed"],"time":"09:30","timezone":"Europe/Istanbul"}`)
	if status != http.StatusOK || view["status"] != "SCHEDULED" {
		t.Fatalf("schedule: %d %+v", status, view)
	}
	rule := view["schedule"].(map[string]any)
	if rule["description"] != "Monday and Wednesday at 09:30 (Europe/Istanbul), reaching only people not reached before" || rule["next_run_at"] == nil {
		t.Fatalf("rule: %+v", rule)
	}
	if status, _ := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/dispatch", ""); status != http.StatusConflict {
		t.Fatalf("send now while scheduled: %d", status)
	}
	status, view = call(t, api, http.MethodDelete, path, "")
	if status != http.StatusOK || view["status"] != "APPROVED" {
		t.Fatalf("cancel: %d %+v", status, view)
	}
	if status, _ := call(t, api, http.MethodDelete, path, ""); status != http.StatusConflict {
		t.Fatalf("cancel again: %d", status)
	}
}

// nextEvent reads one server-sent event, skipping comments.
func nextEvent(t *testing.T, reader *bufio.Reader) sseEvent {
	t.Helper()
	var event sseEvent
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event: "):
			event.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event.data); err != nil {
				t.Fatalf("decode event: %v", err)
			}
		case line == "" && event.name != "":
			return event
		}
	}
}

func TestEventsFollowACampaignWhileItCanChange(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := campaignWithAudience(t, api)
	generate(t, api, id, "")
	call(t, api, http.MethodPost, "/api/campaigns/"+id+"/approve", reviewedBody(t, api, "/api/campaigns/"+id))
	base := "/api/campaigns/" + id
	if status, body := call(t, api, http.MethodPost, base+"/schedule", `{"kind":"weekly","days":["mon"],"time":"09:30","timezone":"UTC"}`); status != http.StatusOK {
		t.Fatalf("schedule: %d %+v", status, body)
	}

	server := httptest.NewServer(api)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+base+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(signIn(request))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type: %s", response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)

	first := nextEvent(t, reader)
	if first.name != "update" || first.data["status"] != "SCHEDULED" || first.data["progress"] == nil {
		t.Fatalf("first event: %+v", first)
	}
	if status, _ := call(t, api, http.MethodDelete, base+"/schedule", ""); status != http.StatusOK {
		t.Fatalf("cancel: %d", status)
	}
	second := nextEvent(t, reader)
	if second.name != "update" || second.data["status"] != "APPROVED" {
		t.Fatalf("change was not pushed: %+v", second)
	}
}

func TestEventsEndForACampaignThatCannotChange(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := campaignWithAudience(t, api)
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, signIn(httptest.NewRequest(http.MethodGet, "/api/campaigns/"+id+"/events", nil)))
	events := readEvents(t, recorder.Body.String())
	if len(events) != 2 || events[0].name != "update" || events[1].name != "end" {
		t.Fatalf("events: %+v", events)
	}
}

func TestEventsForAnUnknownCampaignAreAnOrdinaryError(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	if status, _ := call(t, api, http.MethodGet, "/api/campaigns/00000000-0000-0000-0000-000000000000/events", ""); status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
}
