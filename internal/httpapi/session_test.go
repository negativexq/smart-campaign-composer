package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEveryCampaignRouteNeedsSignIn(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	const id = "00000000-0000-0000-0000-000000000000"
	for _, route := range []string{
		"GET /api/overview", "GET /api/campaigns", "POST /api/campaigns",
		"GET /api/campaigns/" + id, "PATCH /api/campaigns/" + id,
		"POST /api/campaigns/" + id + "/segment", "GET /api/campaigns/" + id + "/audience",
		"POST /api/campaigns/" + id + "/generate", "PUT /api/campaigns/" + id + "/content",
		"GET /api/campaigns/" + id + "/sample-recipients", "POST /api/campaigns/" + id + "/approve",
		"POST /api/campaigns/" + id + "/dispatch", "POST /api/campaigns/" + id + "/schedule",
		"DELETE /api/campaigns/" + id + "/schedule", "GET /api/campaigns/" + id + "/progress",
		"GET /api/campaigns/" + id + "/events", "GET /api/campaigns/" + id + "/deliveries",
		"GET /api/campaigns/" + id + "/deliveries/" + id + "/attempts", "GET /api/campaigns/" + id + "/activity",
	} {
		method, path, _ := strings.Cut(route, " ")
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader("{}")))
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d without a session", route, recorder.Code)
		}
	}
}

func TestSigningInAndOut(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())

	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"identity":"  Marketer@Example.com "}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"marketer@example.com"`) {
		t.Fatalf("sign in: %d %s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie: %+v", cookies)
	}

	read := func(method, path string, withCookie bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, nil)
		if withCookie {
			request.AddCookie(cookies[0])
		}
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}
	if body := read(http.MethodGet, "/api/session", true).Body.String(); !strings.Contains(body, "marketer@example.com") {
		t.Fatalf("session: %s", body)
	}
	if body := read(http.MethodGet, "/api/session", false).Body.String(); !strings.Contains(body, `"identity":null`) {
		t.Fatalf("no session: %s", body)
	}
	if got := read(http.MethodGet, "/api/campaigns", true).Code; got != http.StatusOK {
		t.Fatalf("list with the session: %d", got)
	}
	out := read(http.MethodDelete, "/api/session", true)
	if out.Code != http.StatusNoContent || out.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("sign out: %d %+v", out.Code, out.Result().Cookies())
	}
}

func TestUnusableIdentitiesAreRefused(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	for _, identity := range []string{"", " ", "a", "two words", "tab\\tname", strings.Repeat("x", 255)} {
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{"identity":"`+identity+`"}`)))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%q: %d", identity, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/campaigns", nil)
	request.AddCookie(&http.Cookie{Name: "scc_session", Value: "two%20words"})
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("a bad cookie was accepted: %d", recorder.Code)
	}
}

func TestTheAuditTrailNamesWhoDidEachStep(t *testing.T) {
	t.Parallel()
	api := newAPI(t, fastMock())
	id := campaignWithAudience(t, api)
	generate(t, api, id, "")
	base := "/api/campaigns/" + id
	call(t, api, http.MethodPost, base+"/approve", "")
	if status, body := call(t, api, http.MethodPost, base+"/schedule", `{"kind":"weekly","days":["mon"],"time":"09:30","timezone":"UTC"}`); status != http.StatusOK {
		t.Fatalf("schedule: %d %+v", status, body)
	}

	_, view := call(t, api, http.MethodGet, base, "")
	if view["created_by"] != testIdentity {
		t.Fatalf("created by: %v", view["created_by"])
	}
	if view["schedule"].(map[string]any)["created_by"] != testIdentity {
		t.Fatalf("schedule set up by: %v", view["schedule"])
	}
	_, activity := call(t, api, http.MethodGet, base+"/activity", "")
	seen := map[string]bool{}
	for _, raw := range activity["activity"].([]any) {
		entry := raw.(map[string]any)
		seen[entry["type"].(string)] = true
		if entry["actor_type"] != "user" || entry["actor"] != testIdentity {
			t.Errorf("%s by %v %v", entry["type"], entry["actor_type"], entry["actor"])
		}
	}
	for _, event := range []string{"campaign.created", "content.generated", "campaign.approved", "campaign.scheduled"} {
		if !seen[event] {
			t.Errorf("%s missing from %v", event, seen)
		}
	}
}
