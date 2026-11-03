package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/httpapi"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
	"github.com/negativexq/smart-campaign-composer/internal/unsubscribe"
)

const unsubscribeSecret = "test-unsubscribe-secret"

func unsubscribeAPI(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.NewDatabase(t)
	if _, err := seed.Apply(context.Background(), pool, seed.Generate(referenceTime, 13)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	service := campaign.NewService(pool, ai.NewMock(), telemetry.NewMetrics(prometheus.NewRegistry()),
		noop.NewTracerProvider().Tracer("test"), func() time.Time { return referenceTime })
	mux := http.NewServeMux()
	httpapi.RegisterUnsubscribe(mux, service, unsubscribeSecret)
	return mux, pool
}

func emailConsent(t *testing.T, pool *pgxpool.Pool, user int64) bool {
	t.Helper()
	var allowed bool
	if err := pool.QueryRow(context.Background(), `SELECT allowed FROM consents WHERE user_id = $1 AND channel = 'email'`, user).Scan(&allowed); err != nil {
		t.Fatal(err)
	}
	return allowed
}

func visit(api http.Handler, method, token string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(method, "/api/unsubscribe/"+token, nil))
	return recorder
}

func TestOpeningAnUnsubscribeLinkChangesNothing(t *testing.T) {
	t.Parallel()
	api, pool := unsubscribeAPI(t)
	user := seed.FixtureCartNoPurchase
	// Mail scanners open links, so only the button may withdraw consent.
	got := visit(api, http.MethodGet, unsubscribe.Token(unsubscribeSecret, user))
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "<form") {
		t.Fatalf("%d %s", got.Code, got.Body)
	}
	if !strings.HasPrefix(got.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("type: %s", got.Header().Get("Content-Type"))
	}
	if !emailConsent(t, pool, user) {
		t.Fatal("consent was withdrawn by opening the page")
	}
}

func TestConfirmingWithdrawsOnlyEmailConsentAndCanRepeat(t *testing.T) {
	t.Parallel()
	api, pool := unsubscribeAPI(t)
	user := seed.FixtureCartNoPurchase
	token := unsubscribe.Token(unsubscribeSecret, user)
	for range 2 {
		if got := visit(api, http.MethodPost, token); got.Code != http.StatusOK {
			t.Fatalf("%d %s", got.Code, got.Body)
		}
	}
	if emailConsent(t, pool, user) {
		t.Fatal("consent is still allowed")
	}
	var sms bool
	if err := pool.QueryRow(context.Background(), `SELECT allowed FROM consents WHERE user_id = $1 AND channel = 'sms'`, user).Scan(&sms); err != nil {
		t.Fatal(err)
	}
	if !sms {
		t.Fatal("SMS consent must not change")
	}
}

func TestAForgedUnsubscribeLinkDoesNothing(t *testing.T) {
	t.Parallel()
	api, pool := unsubscribeAPI(t)
	user := seed.FixtureCartNoPurchase
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if got := visit(api, method, unsubscribe.Token("wrong", user)); got.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", method, got.Code)
		}
	}
	if !emailConsent(t, pool, user) {
		t.Fatal("a forged link withdrew consent")
	}
}

func TestEmailPreviewRendersADraft(t *testing.T) {
	t.Parallel()
	api := newAPI(t, ai.NewMock())
	status, body := call(t, api, http.MethodPost, "/api/email-preview",
		`{"first_name":"Ayşe","email_subject":"Hi {{first_name}}","email_headline":"<b>News</b>","email_body":"Hello {{first_name}}"}`)
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, body)
	}
	if body["subject"] != "Hi Ayşe" || !strings.Contains(body["text"].(string), "Hello Ayşe") {
		t.Fatalf("body: %v", body)
	}
	if html := body["html"].(string); !strings.Contains(html, "Hello Ayşe") || strings.Contains(html, "<b>News") {
		t.Fatalf("html: %s", html)
	}
}
