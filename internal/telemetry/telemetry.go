// Package telemetry holds the operational signals: Prometheus metrics, JSON logs
// and OpenTelemetry traces. Business history lives in audit_events instead, so it
// does not depend on how long any of this is retained.
//
// Labels and span attributes must stay free of personal data and message text.
package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Metrics are the application's Prometheus series.
type Metrics struct {
	LLMRequests        *prometheus.CounterVec
	LLMDuration        *prometheus.HistogramVec
	LLMTTFT            *prometheus.HistogramVec
	LLMTokens          *prometheus.CounterVec
	ValidationFailures *prometheus.CounterVec
	HTTPDuration       *prometheus.HistogramVec
	SSEConnections     prometheus.Gauge

	DeliveryAttempts      *prometheus.CounterVec
	DeliveryDuration      *prometheus.HistogramVec
	DeliveryDuplicates    *prometheus.CounterVec
	DeliveryQueueDepth    *prometheus.GaugeVec
	DeliveryRetries       *prometheus.CounterVec
	DeliveryRecovered     *prometheus.CounterVec
	DeliveryRateLimitWait *prometheus.HistogramVec
	DeliveryCircuitOpen   *prometheus.GaugeVec
	DeliveryCircuitTrips  *prometheus.CounterVec
	// DeliveryCredentialLocks counts channels held because the provider refused
	// their credentials; DeliveryAlerts counts channel alerts opening and closing.
	DeliveryCredentialLocks *prometheus.CounterVec
	DeliveryAlerts          *prometheus.CounterVec

	// These split a channel's time and a message's wait into what each one went on.
	DeliveryLoopSeconds    *prometheus.CounterVec
	DeliveryClaimDuration  *prometheus.HistogramVec
	DeliveryRecordDuration *prometheus.HistogramVec
	DeliveryQueueAge       *prometheus.HistogramVec
	DeliveryGrantToSend    *prometheus.HistogramVec
	DeliveryInflight       *prometheus.GaugeVec
	LimiterReserveDuration *prometheus.HistogramVec
	LimiterSettleDuration  *prometheus.HistogramVec
	LimiterGranted         *prometheus.CounterVec
	LimiterUnused          *prometheus.CounterVec
	LimiterActualWait      *prometheus.HistogramVec
	LimiterPause           *prometheus.HistogramVec
}

// quickBuckets suit calls that normally take milliseconds: Redis, the queue, a commit.
var quickBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// NewMetrics registers the series on registerer.
func NewMetrics(registerer prometheus.Registerer) *Metrics {
	factory := promauto.With(registerer)
	return &Metrics{
		LLMRequests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_llm_requests_total",
			Help: "LLM calls by result. success means the output also passed validation.",
		}, []string{"operation", "provider", "outcome"}),
		LLMDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_llm_duration_seconds",
			Help:    "Duration of LLM calls.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30, 60, 120},
		}, []string{"operation", "provider"}),
		LLMTTFT: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_llm_ttft_seconds",
			Help:    "Time until a streaming LLM call produced its first text.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30},
		}, []string{"operation", "provider"}),
		LLMTokens: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_llm_tokens_total",
			Help: "Tokens used by LLM calls.",
		}, []string{"operation", "provider", "direction"}),
		ValidationFailures: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_validation_failures_total",
			Help: "Generated output that was rejected, or that blocks approval, by fixed reason code.",
		}, []string{"stage", "reason"}),
		HTTPDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_request_duration_seconds",
			Help:    "Duration of HTTP requests by route pattern.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route", "status"}),
		SSEConnections: factory.NewGauge(prometheus.GaugeOpts{
			Name: "http_sse_connections",
			Help: "Server-sent event streams currently open.",
		}),
		DeliveryAttempts: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_delivery_attempts_total",
			Help: "Calls to the provider by result. accepted means the provider took the message, not that a person received it.",
		}, []string{"channel", "outcome"}),
		DeliveryDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_delivery_duration_seconds",
			Help:    "Duration of calls to the provider.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
		}, []string{"channel"}),
		DeliveryDuplicates: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_delivery_duplicates_prevented_total",
			Help: "Repeated sends the provider recognised by idempotency key and did not deliver again.",
		}, []string{"channel"}),
		DeliveryQueueDepth: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "campaign_delivery_queue_depth",
			Help: "Deliveries waiting to be sent or retried.",
		}, []string{"channel"}),
		DeliveryRecovered: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_delivery_recovered_total",
			Help: "Deliveries taken back after their worker stopped without recording an outcome.",
		}, []string{"channel"}),
		DeliveryRetries: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_delivery_retry_total",
			Help: "Deliveries scheduled for another try, by what happened.",
		}, []string{"channel", "reason"}),
		DeliveryRateLimitWait: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_delivery_rate_limit_wait_seconds",
			Help:    "How long a channel was told to wait each time the shared rate limit had nothing to grant.",
			Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
		}, []string{"channel"}),
		DeliveryCircuitOpen: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "campaign_delivery_circuit_open",
			Help: "1 while a channel has stopped calling a failing provider.",
		}, []string{"channel"}),
		DeliveryCircuitTrips: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_delivery_circuit_trips_total",
			Help: "Times a channel stopped calling a failing provider.",
		}, []string{"channel"}),
		DeliveryCredentialLocks: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_delivery_credential_locks_total",
			Help: "Times a channel was held because the provider refused its credentials.",
		}, []string{"channel"}),
		DeliveryAlerts: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_delivery_alerts_total",
			Help: "Channel alerts on sending campaigns, by kind and whether they opened or closed.",
		}, []string{"channel", "alert", "state"}),
		DeliveryLoopSeconds: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_delivery_loop_seconds_total",
			Help: "Time a channel's sending loop spent, by state. Together the states add up to the time it ran.",
		}, []string{"channel", "state"}),
		DeliveryClaimDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_delivery_claim_duration_seconds",
			Help:    "Time claiming from the queue, by stage: acquire, execute, read, decode and total.",
			Buckets: quickBuckets,
		}, []string{"channel", "stage"}),
		DeliveryRecordDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_delivery_record_duration_seconds",
			Help:    "Time storing an outcome, from taking a connection to the commit.",
			Buckets: quickBuckets,
		}, []string{"channel"}),
		DeliveryQueueAge: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_delivery_queue_age_seconds",
			Help:    "How long a delivery had been due when it was claimed.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 15, 30, 60, 120, 300, 600, 1200, 3600},
		}, []string{"channel"}),
		DeliveryGrantToSend: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_delivery_grant_to_send_seconds",
			Help:    "Time from the limiter's permit to the start of the provider call.",
			Buckets: quickBuckets,
		}, []string{"channel"}),
		DeliveryInflight: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "campaign_delivery_inflight",
			Help: "Calls to the provider under way.",
		}, []string{"channel"}),
		LimiterReserveDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_limiter_reserve_duration_seconds",
			Help:    "Time asking the limiter for permits.",
			Buckets: quickBuckets,
		}, []string{"channel"}),
		LimiterSettleDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_limiter_settle_duration_seconds",
			Help:    "Time settling permits with the limiter.",
			Buckets: quickBuckets,
		}, []string{"channel"}),
		LimiterGranted: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_limiter_granted_total",
			Help: "Permits the limiter granted.",
		}, []string{"channel"}),
		LimiterUnused: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "campaign_limiter_unused_total",
			Help: "Permits given back because the queue had too little to claim.",
		}, []string{"channel"}),
		LimiterActualWait: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_limiter_actual_wait_seconds",
			Help:    "How long a channel really waited with nothing granted, by reason: pause, window or breaker.",
			Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		}, []string{"channel", "reason"}),
		LimiterPause: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "campaign_limiter_pause_seconds",
			Help:    "Pause lengths asked of the limiter, by source. They can overlap, so compare with the actual wait.",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		}, []string{"channel", "source"}),
	}
}

// NewLogger writes JSON logs that carry the trace ID when a span is active.
func NewLogger(output io.Writer) *slog.Logger {
	return slog.New(traceHandler{slog.NewJSONHandler(output, nil)})
}

type traceHandler struct {
	slog.Handler
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		record.AddAttrs(slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
	}
	return h.Handler.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}

// SetupTracing exports spans over OTLP when OTEL_EXPORTER_OTLP_ENDPOINT is set
// and leaves tracing off otherwise. Sampling follows the standard OTEL_TRACES_*
// variables.
func SetupTracing(ctx context.Context, service string, getenv func(string) string) (func(context.Context) error, error) {
	if getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	serviceResource, err := resource.Merge(resource.Default(), resource.NewSchemaless(attribute.String("service.name", service)))
	if err != nil {
		return nil, fmt.Errorf("build trace resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(serviceResource))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return provider.Shutdown, nil
}

var (
	knownMethods  = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	unloggedPaths = []string{"/metrics", "/healthz", "/readyz"}
)

// Middleware measures, traces and logs requests. It labels by route pattern, so
// IDs and scanner noise cannot grow the series count.
func Middleware(metrics *Metrics, logger *slog.Logger, next http.Handler) http.Handler {
	tracer := otel.Tracer("http")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx, span := tracer.Start(r.Context(), "http.request")
		defer span.End()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// The mux sets Pattern on the request it receives, so keep that copy.
		routed := r.WithContext(ctx)
		next.ServeHTTP(recorder, routed)

		route := routeOf(routed)
		method := r.Method
		if !slices.Contains(knownMethods, method) {
			method = "OTHER"
		}
		duration := time.Since(start)
		metrics.HTTPDuration.WithLabelValues(method, route, strconv.Itoa(recorder.status)).Observe(duration.Seconds())
		span.SetName(method + " " + route)
		span.SetAttributes(attribute.Int("http.response.status_code", recorder.status))

		if !slices.Contains(unloggedPaths, r.URL.Path) {
			logger.InfoContext(ctx, "request", "method", method, "route", route,
				"status", recorder.status, "duration_ms", duration.Milliseconds())
		}
	})
}

func routeOf(r *http.Request) string {
	if r.Pattern == "" {
		return "unmatched"
	}
	if _, route, found := strings.Cut(r.Pattern, " "); found {
		return route
	}
	return r.Pattern
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach Flush for SSE.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
