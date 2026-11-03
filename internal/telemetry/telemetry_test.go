package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

func TestHTTPMetricsUseTheRoutePatternNotThePath(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	metrics := telemetry.NewMetrics(registry)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/campaigns/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := telemetry.Middleware(metrics, slog.New(slog.DiscardHandler), mux)

	for _, id := range []string{"one", "two"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/campaigns/"+id, nil))
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/scan/wp-admin.php", nil))

	output, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	for _, family := range output {
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "route" {
					routes = append(routes, label.GetValue())
				}
			}
		}
	}
	joined := strings.Join(routes, ",")
	if strings.Contains(joined, "one") || strings.Contains(joined, "wp-admin") {
		t.Fatalf("raw path reached a label: %s", joined)
	}
	if !strings.Contains(joined, "/api/campaigns/{id}") || !strings.Contains(joined, "unmatched") {
		t.Fatalf("routes: %s", joined)
	}
	if got := testutil.CollectAndCount(metrics.HTTPDuration); got != 2 {
		t.Fatalf("series: %d", got)
	}
}

func TestMiddlewareKeepsStreamingPossible(t *testing.T) {
	t.Parallel()
	metrics := telemetry.NewMetrics(prometheus.NewRegistry())
	flushed := false
	handler := telemetry.Middleware(metrics, slog.New(slog.DiscardHandler), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushed = http.NewResponseController(w).Flush() == nil
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !flushed {
		t.Fatal("wrapped writer must still flush for SSE")
	}
}

func TestLogsAreJSONAndCarryTheTraceID(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := telemetry.NewLogger(&output)

	provider := sdktrace.NewTracerProvider()
	ctx, span := provider.Tracer("test").Start(context.Background(), "work")
	logger.InfoContext(ctx, "hello")
	span.End()

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("not json: %v: %s", err, output.String())
	}
	if entry["trace_id"] != span.SpanContext().TraceID().String() {
		t.Fatalf("trace id: %v", entry["trace_id"])
	}
}

func TestLogsWithoutASpanHaveNoTraceID(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	telemetry.NewLogger(&output).InfoContext(context.Background(), "hello")
	if strings.Contains(output.String(), "trace_id") {
		t.Fatalf("unexpected trace id: %s", output.String())
	}
}

func TestTracingIsOffWithoutAnEndpoint(t *testing.T) {
	t.Parallel()
	shutdown, err := telemetry.SetupTracing(context.Background(), "test", func(string) string { return "" })
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
