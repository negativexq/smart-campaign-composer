package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/health"
)

func get(t *testing.T, handler http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func pass(context.Context) error { return nil }

func TestLivenessDoesNotDependOnChecks(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	health.Register(mux, map[string]health.Check{
		"postgres": func(context.Context) error { return errors.New("down") },
	})

	if code, _ := get(t, mux, "/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz: got %d, want 200", code)
	}
}

func TestReadinessPassesWhenAllChecksPass(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	health.Register(mux, map[string]health.Check{"postgres": pass, "redis": pass})

	code, body := get(t, mux, "/readyz")

	if code != http.StatusOK {
		t.Fatalf("/readyz: got %d, want 200", code)
	}
	var got struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if got.Status != "ok" || got.Checks["postgres"] != "ok" || got.Checks["redis"] != "ok" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestReadinessReportsFailingDependencyWithoutLeakingDetails(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	health.Register(mux, map[string]health.Check{
		"postgres": pass,
		"redis":    func(context.Context) error { return errors.New("dial redis://:secret@host") },
	})

	code, body := get(t, mux, "/readyz")

	if code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz: got %d, want 503", code)
	}
	if strings.Contains(body, "secret") {
		t.Fatalf("response leaks error details: %s", body)
	}
	if !strings.Contains(body, `"redis":"down"`) || !strings.Contains(body, `"postgres":"ok"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestReadinessTimesOutHangingCheck(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	health.Register(mux, map[string]health.Check{
		"postgres": func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})

	start := time.Now()
	code, _ := get(t, mux, "/readyz")

	if code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz: got %d, want 503", code)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("hanging check blocked readiness for %v", elapsed)
	}
}

func TestSystemSummaryAlwaysAnswersAndNamesWhatIsDown(t *testing.T) {
	t.Parallel()
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer alive.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer broken.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	mux := http.NewServeMux()
	health.RegisterSystem(mux, "/api/system", map[string]health.Check{
		"worker":   health.HTTPCheck(alive.URL),
		"provider": health.HTTPCheck(broken.URL),
		"redis":    health.HTTPCheck(gone.URL),
	})
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/system", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d", recorder.Code)
	}
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status == "ok" || body.Checks["worker"] != "ok" || body.Checks["provider"] != "down" || body.Checks["redis"] != "down" {
		t.Fatalf("body: %+v", body)
	}
}
