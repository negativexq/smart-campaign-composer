package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/httpapi"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

var referenceTime = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

const exampleBrief = "İstanbul'da son 30 günde sepete ürün ekleyip alışveriş yapmayan kullanıcılara hatırlatma gönder"

// failingProvider stands in for a provider outage.
type failingProvider struct {
	ai.Provider
	err error
}

func (p failingProvider) ProposeSegment(context.Context, ai.SegmentRequest) (ai.SegmentResponse, error) {
	return ai.SegmentResponse{}, p.err
}

func newAPI(t *testing.T, provider ai.Provider) http.Handler {
	t.Helper()
	pool := testsupport.NewDatabase(t)
	if _, err := seed.Apply(context.Background(), pool, seed.Generate(referenceTime, 13)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service := campaign.NewService(pool, provider, telemetry.NewMetrics(prometheus.NewRegistry()),
		noop.NewTracerProvider().Tracer("test"), func() time.Time { return referenceTime })
	mux := http.NewServeMux()
	httpapi.Register(mux, service, provider)
	return mux
}

func call(t *testing.T, api http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("%s %s: response is not JSON: %q", method, path, recorder.Body.String())
	}
	return recorder.Code, decoded
}

func createCampaign(t *testing.T, api http.Handler, brief string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"brief": brief})
	status, response := call(t, api, http.MethodPost, "/api/campaigns", string(body))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, response)
	}
	return response["id"].(string)
}

func TestBriefToAudience(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := createCampaign(t, api, exampleBrief)

	status, response := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", `{}`)
	if status != http.StatusOK {
		t.Fatalf("segment: %d %v", status, response)
	}
	if conditions := response["conditions"].([]any); len(conditions) != 3 {
		t.Fatalf("conditions: %v", conditions)
	}

	status, audience := call(t, api, http.MethodGet, "/api/campaigns/"+id+"/audience", "")
	if status != http.StatusOK || audience["total"].(float64) != 9 || audience["sms"].(float64) != 7 {
		t.Fatalf("audience: %d %v", status, audience)
	}

	status, campaignView := call(t, api, http.MethodGet, "/api/campaigns/"+id, "")
	if status != http.StatusOK || campaignView["status"] != "DRAFT" || campaignView["brief"] != exampleBrief {
		t.Fatalf("campaign: %d %v", status, campaignView)
	}
}

func TestRefinementThroughTheAPI(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := createCampaign(t, api, exampleBrief)
	call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", `{}`)

	status, response := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", `{"refinement":"make it 4 days"}`)
	if status != http.StatusOK {
		t.Fatalf("refine: %d %v", status, response)
	}
	// Only the fixtures with a cart event newer than 4 days remain: one user.
	if total := response["audience"].(map[string]any)["total"].(float64); total != 1 {
		t.Fatalf("total: %v", total)
	}
}

func TestClientErrors(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := createCampaign(t, api, exampleBrief)
	tests := []struct {
		name, method, path, body string
		status                   int
	}{
		{"empty brief", http.MethodPost, "/api/campaigns", `{"brief":" "}`, http.StatusBadRequest},
		{"malformed body", http.MethodPost, "/api/campaigns", `{`, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/api/campaigns", `{"brief":"x","where":"1=1"}`, http.StatusBadRequest},
		{"unknown campaign", http.MethodGet, "/api/campaigns/00000000-0000-0000-0000-000000000000", "", http.StatusNotFound},
		{"bad id", http.MethodGet, "/api/campaigns/nope", "", http.StatusNotFound},
		{"audience before segment", http.MethodGet, "/api/campaigns/" + id + "/audience", "", http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, response := call(t, api, tt.method, tt.path, tt.body)
			if status != tt.status {
				t.Fatalf("got %d, want %d: %v", status, tt.status, response)
			}
			if message, _ := response["error"].(string); message == "" {
				t.Fatalf("no error message: %v", response)
			}
		})
	}
}

func TestBriefTheProviderCannotUseIsActionable(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	id := createCampaign(t, api, "hello there")

	status, response := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", `{}`)

	if status != http.StatusUnprocessableEntity {
		t.Fatalf("got %d: %v", status, response)
	}
	if !strings.Contains(response["error"].(string), "at least one") {
		t.Fatalf("message: %v", response["error"])
	}
}

func TestProviderFailuresBecomeHonestResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err    error
		status int
	}{
		{ai.ErrRefused, http.StatusUnprocessableEntity},
		{ai.ErrTruncated, http.StatusBadGateway},
		{ai.ErrInvalidOutput, http.StatusBadGateway},
		{ai.ErrRateLimited, http.StatusServiceUnavailable},
		{ai.ErrUnavailable, http.StatusServiceUnavailable},
		{errors.New("pq: secret internal detail"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			t.Parallel()
			api := newAPI(t, failingProvider{ai.NewMock(), tt.err})
			id := createCampaign(t, api, exampleBrief)
			status, response := call(t, api, http.MethodPost, "/api/campaigns/"+id+"/segment", `{}`)
			if status != tt.status {
				t.Fatalf("got %d, want %d", status, tt.status)
			}
			if strings.Contains(response["error"].(string), "secret") {
				t.Fatalf("internal detail leaked: %v", response)
			}
		})
	}
}

func TestConfigNamesTheProviderForTheBadge(t *testing.T) {
	t.Parallel()
	status, response := call(t, newAPI(t, ai.NewMock()), http.MethodGet, "/api/config", "")
	if status != http.StatusOK || response["llm_provider"] != "mock" || response["llm_model"] == "" {
		t.Fatalf("config: %d %v", status, response)
	}
}
