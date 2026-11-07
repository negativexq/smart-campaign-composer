package httpapi_test

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
)

func sentCampaign(t *testing.T, api http.Handler) string {
	t.Helper()
	id := campaignWithAudience(t, api)
	if got := generate(t, api, id, `{}`); got.Code != http.StatusOK {
		t.Fatalf("generate: %d", got.Code)
	}
	base := "/api/campaigns/" + id
	if status, view := call(t, api, http.MethodPost, base+"/approve", reviewedBody(t, api, base)); status >= 300 {
		t.Fatalf("approve: %d %v", status, view)
	}
	if status, view := call(t, api, http.MethodPost, base+"/dispatch", ""); status >= 300 {
		t.Fatalf("dispatch: %d %v", status, view)
	}
	return id
}

func export(t *testing.T, api http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, signIn(httptest.NewRequest(http.MethodGet, path, nil)))
	return recorder
}

func TestExportHandsOverEveryDeliveryWithoutContactDetails(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := sentCampaign(t, api)

	got := export(t, api, "/api/campaigns/"+id+"/deliveries.csv")
	if got.Code != http.StatusOK || !strings.Contains(got.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("export: %d %s", got.Code, got.Header())
	}
	if !strings.Contains(got.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("headers: %v", got.Header())
	}
	body := got.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf") {
		t.Fatal("no byte order mark for Excel")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rows[0][0] != "user_id" {
		t.Fatalf("header: %v", rows[0])
	}
	if strings.Contains(body, "@example") || strings.Contains(rows[0][1], "email") {
		t.Fatalf("contact details left in the export: %.300s", body)
	}
	_, progress := call(t, api, http.MethodGet, "/api/campaigns/"+id+"/progress", "")
	if total := int(progress["total"].(float64)); len(rows)-1 != total {
		t.Fatalf("%d rows for %d deliveries", len(rows)-1, total)
	}

	sms := export(t, api, "/api/campaigns/"+id+"/deliveries.csv?channel=sms")
	filtered, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(sms.Body.String(), "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) >= len(rows) || len(filtered) < 2 {
		t.Fatalf("filter kept %d of %d", len(filtered), len(rows))
	}
	for _, row := range filtered[1:] {
		if row[2] != "sms" {
			t.Fatalf("filter let through: %v", row)
		}
	}
}

func TestExportRefusesWhatTheListRefuses(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := sentCampaign(t, api)
	if got := export(t, api, "/api/campaigns/"+id+"/deliveries.csv?status=NOPE"); got.Code != http.StatusBadRequest {
		t.Fatalf("bad filter: %d", got.Code)
	}
	if got := export(t, api, "/api/campaigns/00000000-0000-0000-0000-000000000000/deliveries.csv"); got.Code != http.StatusNotFound {
		t.Fatalf("unknown campaign: %d", got.Code)
	}
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/campaigns/"+id+"/deliveries.csv", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", recorder.Code)
	}
}

func TestExportLeavesARecord(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := sentCampaign(t, api)
	export(t, api, "/api/campaigns/"+id+"/deliveries.csv")
	recorder := export(t, api, "/api/campaigns/"+id+"/activity")
	if !strings.Contains(recorder.Body.String(), "deliveries.exported") {
		t.Fatalf("activity: %.400s", recorder.Body.String())
	}
}

func TestDuplicateStartsANewDraftFromTheOriginal(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := sentCampaign(t, api)
	_, source := call(t, api, http.MethodGet, "/api/campaigns/"+id, "")

	status, copied := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/duplicate", "")
	if status != http.StatusCreated {
		t.Fatalf("duplicate: %d %v", status, copied)
	}
	if copied["id"] == id || copied["status"] != "READY_FOR_REVIEW" || copied["approved_at"] != nil {
		t.Fatalf("copy: %v", copied)
	}
	if !strings.HasSuffix(copied["name"].(string), "(copy)") || copied["brief"] != source["brief"] {
		t.Fatalf("copy: %v %v", copied["name"], copied["brief"])
	}
	first, second := source["content"].(map[string]any), copied["content"].(map[string]any)
	if first["sms"] != second["sms"] {
		t.Fatalf("messages: %v %v", first["sms"], second["sms"])
	}
	newID := copied["id"].(string)
	_, progress := call(t, api, http.MethodGet, "/api/campaigns/"+newID+"/progress", "")
	if progress["total"].(float64) != 0 {
		t.Fatalf("deliveries came along: %v", progress)
	}
	// Nothing goes out until the copy is approved, and approving it works.
	if status, _ := call(t, api, http.MethodPost, "/api/campaigns/"+newID+"/dispatch", ""); status < 300 {
		t.Fatalf("an unapproved copy was sent: %d", status)
	}
	if status, view := call(t, api, http.MethodPost, "/api/campaigns/"+newID+"/approve", reviewedBody(t, api, "/api/campaigns/"+newID)); status != http.StatusOK {
		t.Fatalf("approve copy: %d %v", status, view)
	}
	// The original stays as it was.
	_, after := call(t, api, http.MethodGet, "/api/campaigns/"+id, "")
	if after["status"] != source["status"] || after["name"] != source["name"] {
		t.Fatalf("original changed: %v", after["status"])
	}
}

// The preview and the rule a copy is made with reach the API.
func TestACopyCanBeMadeWithARuleForPeopleMessagedLately(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := sentCampaign(t, api)

	status, preview := call(t, api, http.MethodGet, "/api/campaigns/"+id+"/duplicate-preview?not_messaged_within_days=10", "")
	if status != http.StatusOK || preview["not_messaged_within_days"].(float64) != 10 || preview["audience"] == nil {
		t.Fatalf("preview: %d %v", status, preview)
	}
	for _, days := range []string{"0", "366", "ten"} {
		if status, _ := call(t, api, http.MethodGet, "/api/campaigns/"+id+"/duplicate-preview?not_messaged_within_days="+days, ""); status != http.StatusBadRequest {
			t.Fatalf("a rule of %s days: %d", days, status)
		}
	}
	status, copied := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/duplicate", `{"not_messaged_within_days": 10}`)
	if status != http.StatusCreated || copied["not_messaged_within_days"].(float64) != 10 {
		t.Fatalf("duplicate: %d %v", status, copied["not_messaged_within_days"])
	}
	segment := copied["proposal"].(map[string]any)["segment"].(map[string]any)
	if segment["not_messaged_within_days"].(float64) != 10 || copied["audience"].(map[string]any)["total"] != preview["audience"].(map[string]any)["total"] {
		t.Fatalf("the copy's audience is not the one previewed: %v %v", segment, copied["audience"])
	}
}

func TestADraftWithoutAnAudienceCanBeDuplicated(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := createCampaign(t, api, exampleBrief)
	status, copied := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/duplicate", "")
	if status != http.StatusCreated || copied["status"] != "DRAFT" || copied["content"] != nil {
		t.Fatalf("copy: %d %v", status, copied)
	}
	if status, _ := call(t, api, http.MethodPost, "/api/campaigns/00000000-0000-0000-0000-000000000000/duplicate", ""); status != http.StatusNotFound {
		t.Fatalf("unknown campaign: %d", status)
	}
}

func TestASentCampaignListsItsRunsAndReadsOneOnItsOwn(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := sentCampaign(t, api)

	status, body := call(t, api, http.MethodGet, "/api/campaigns/"+id+"/runs", "")
	runs, _ := body["runs"].([]any)
	if status != http.StatusOK || len(runs) != 1 || runs[0].(map[string]any)["number"].(float64) != 1 {
		t.Fatalf("runs: %d %v", status, body)
	}
	for _, path := range []string{"/progress?run=1", "/report?run=1", "/deliveries?run=1&limit=5"} {
		if status, _ := call(t, api, http.MethodGet, "/api/campaigns/"+id+path, ""); status != http.StatusOK {
			t.Fatalf("%s: %d", path, status)
		}
	}
	if status, _ := call(t, api, http.MethodGet, "/api/campaigns/"+id+"/progress?run=2", ""); status != http.StatusNotFound {
		t.Fatalf("a run that does not exist: %d", status)
	}
	for _, run := range []string{"0", "-1", "one"} {
		if status, _ := call(t, api, http.MethodGet, "/api/campaigns/"+id+"/deliveries?run="+run, ""); status != http.StatusBadRequest {
			t.Fatalf("run %s: %d", run, status)
		}
	}

	status, list := call(t, api, http.MethodGet, "/api/campaigns?group=sending", "")
	groups, _ := list["groups"].(map[string]any)
	if status != http.StatusOK || groups == nil || groups["all"].(float64) < 1 || list["upcoming"] == nil {
		t.Fatalf("list: %d %v", status, list)
	}
	if status, _ := call(t, api, http.MethodGet, "/api/campaigns?group=nope", ""); status != http.StatusBadRequest {
		t.Fatalf("unknown group: %d", status)
	}
}

func TestCampaignsCanBeTaggedArchivedAndDeletedThroughTheAPI(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	sent := sentCampaign(t, api)
	draft := createCampaign(t, api, "Remind people in İstanbul about their carts from the last 30 days")

	status, body := call(t, api, http.MethodPut, "/api/campaigns/"+sent+"/tags", `{"tags":["Winback"," winback ","Q4"]}`)
	if status != http.StatusOK || len(body["tags"].([]any)) != 2 {
		t.Fatalf("tags: %d %v", status, body)
	}
	if status, _ := call(t, api, http.MethodPut, "/api/campaigns/"+sent+"/tags", `{"tags":[""]}`); status != http.StatusBadRequest {
		t.Fatalf("empty tag: %d", status)
	}
	status, body = call(t, api, http.MethodGet, "/api/campaign-tags", "")
	if tags, _ := body["tags"].([]any); status != http.StatusOK || len(tags) != 2 {
		t.Fatalf("tag list: %d %v", status, body)
	}

	// A campaign that sent keeps its record, so it is archived rather than deleted.
	if status, _ := call(t, api, http.MethodDelete, "/api/campaigns/"+sent, ""); status != http.StatusConflict {
		t.Fatalf("delete a sent campaign: %d", status)
	}
	if status, _ := call(t, api, http.MethodDelete, "/api/campaigns/"+sent+"/archive", ""); status != http.StatusConflict {
		t.Fatalf("restore a campaign that is not archived: %d", status)
	}

	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, signIn(httptest.NewRequest(http.MethodDelete, "/api/campaigns/"+draft, nil)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete a draft: %d %s", recorder.Code, recorder.Body)
	}
	if status, _ := call(t, api, http.MethodGet, "/api/campaigns/"+draft, ""); status != http.StatusNotFound {
		t.Fatalf("get a deleted draft: %d", status)
	}
}
