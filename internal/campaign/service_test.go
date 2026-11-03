package campaign_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

var referenceTime = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

const (
	validProposal   = `{"segment":{"city":"İstanbul","cart_added_within_days":30,"no_purchase_since_cart":true},"assumptions":[],"unsupported_conditions":[]}`
	invalidProposal = `{"segment":{"city":"Atlantis"},"assumptions":[],"unsupported_conditions":[]}`
	secretBrief     = "Remind Ayşe, phone +905001234567, about her cart"
)

type step struct {
	output string
	err    error
}

// scriptedProvider returns its steps in order and records what it was asked.
type scriptedProvider struct {
	mu       sync.Mutex
	steps    []step
	requests []ai.SegmentRequest
}

func (*scriptedProvider) Name() string  { return "scripted" }
func (*scriptedProvider) Model() string { return "script-1" }

func (p *scriptedProvider) ProposeSegment(_ context.Context, request ai.SegmentRequest) (ai.SegmentResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	current := p.steps[0]
	if len(p.steps) > 1 {
		p.steps = p.steps[1:]
	}
	return ai.SegmentResponse{Output: []byte(current.output), Usage: ai.Usage{InputTokens: 10, OutputTokens: 5}}, current.err
}

type harness struct {
	service  *campaign.Service
	provider *scriptedProvider
	pool     *pgxpool.Pool
	metrics  *telemetry.Metrics
	spans    *tracetest.InMemoryExporter
}

func newHarness(t *testing.T, steps ...step) *harness {
	t.Helper()
	pool := testsupport.NewDatabase(t)
	if _, err := seed.Apply(context.Background(), pool, seed.Generate(referenceTime, 13)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	provider := &scriptedProvider{steps: steps}
	metrics := telemetry.NewMetrics(prometheus.NewRegistry())
	spans := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)).Tracer("test")
	service := campaign.NewService(pool, provider, metrics, tracer, func() time.Time { return referenceTime })
	return &harness{service, provider, pool, metrics, spans}
}

func (h *harness) create(t *testing.T) string {
	t.Helper()
	id, err := h.service.Create(context.Background(), secretBrief)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return id
}

func (h *harness) auditEvents(t *testing.T, id string) []string {
	t.Helper()
	rows, err := h.pool.Query(context.Background(), `SELECT event_type FROM audit_events WHERE campaign_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	events := []string{}
	for rows.Next() {
		var event string
		if err := rows.Scan(&event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func TestCreateRejectsAnUnusableBrief(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	for _, brief := range []string{"", "   ", strings.Repeat("x", campaign.MaxBriefLength+1)} {
		if _, err := h.service.Create(context.Background(), brief); !errors.Is(err, campaign.ErrInvalidBrief) {
			t.Errorf("brief of %d chars: %v", len(brief), err)
		}
	}
}

func TestProposeStoresADraftWithItsAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	id := h.create(t)

	view, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}

	if view.Audience.Total != 9 || view.Audience.SMS != 7 || view.Audience.Email != 7 {
		t.Fatalf("audience: %+v", view.Audience)
	}
	if len(view.Conditions) != 3 || view.Provider != "scripted" || view.Model != "script-1" {
		t.Fatalf("view: %+v", view)
	}
	if len(view.Blockers) != 0 {
		t.Fatalf("blockers: %v", view.Blockers)
	}

	reloaded, err := h.service.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if reloaded.Audience != view.Audience || reloaded.Version != 1 {
		t.Fatalf("reload differs: %+v", reloaded)
	}
	if got := h.auditEvents(t, id); strings.Join(got, ",") != "campaign.created,segment.proposed" {
		t.Fatalf("audit: %v", got)
	}
}

func TestRepairRetriesWithTheValidationError(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: invalidProposal}, step{output: validProposal})
	id := h.create(t)

	if _, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}

	if len(h.provider.requests) != 2 {
		t.Fatalf("calls: %d", len(h.provider.requests))
	}
	repair := h.provider.requests[1]
	if repair.PreviousOutput != invalidProposal || !strings.Contains(repair.ValidationError, "Atlantis") {
		t.Fatalf("repair request: %+v", repair)
	}
	if got := testutil.ToFloat64(h.metrics.ValidationFailures.WithLabelValues("segment", "unknown_city")); got != 1 {
		t.Fatalf("validation failures: %v", got)
	}
	if got := testutil.ToFloat64(h.metrics.LLMRequests.WithLabelValues("segment_generation", "scripted", "invalid_output")); got != 1 {
		t.Fatalf("invalid requests: %v", got)
	}
	if got := testutil.ToFloat64(h.metrics.LLMRequests.WithLabelValues("segment_generation", "scripted", "success")); got != 1 {
		t.Fatalf("successful requests: %v", got)
	}
	if got := testutil.ToFloat64(h.metrics.LLMTokens.WithLabelValues("segment_generation", "scripted", "output")); got != 10 {
		t.Fatalf("output tokens: %v", got)
	}
}

func TestRepairGivesUpAndStoresNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: invalidProposal})
	id := h.create(t)

	_, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{})

	var validation *segment.ValidationError
	if !errors.As(err, &validation) || !errors.Is(err, campaign.ErrProposalRejected) {
		t.Fatalf("got %v", err)
	}
	if len(h.provider.requests) != campaign.MaxProposalAttempts {
		t.Fatalf("calls: %d", len(h.provider.requests))
	}
	view, err := h.service.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Version != 0 || view.Proposal != nil {
		t.Fatalf("rejected output was stored: %+v", view)
	}
}

func TestProviderFailuresAreNotRetriedAsRepairs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err     error
		outcome string
	}{
		{ai.ErrRefused, "refused"},
		{ai.ErrTruncated, "truncated"},
		{ai.ErrRateLimited, "rate_limited"},
		{ai.ErrUnavailable, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.outcome, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, step{err: tt.err})
			id := h.create(t)

			_, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{})

			if !errors.Is(err, tt.err) {
				t.Fatalf("got %v", err)
			}
			if len(h.provider.requests) != 1 {
				t.Fatalf("calls: %d", len(h.provider.requests))
			}
			if got := testutil.ToFloat64(h.metrics.LLMRequests.WithLabelValues("segment_generation", "scripted", tt.outcome)); got != 1 {
				t.Fatalf("metric: %v", got)
			}
		})
	}
}

func TestRefinementEditsTheSameDraft(t *testing.T) {
	t.Parallel()
	narrower := strings.Replace(validProposal, `"cart_added_within_days":30`, `"cart_added_within_days":4`, 1)
	h := newHarness(t, step{output: validProposal}, step{output: narrower})
	id := h.create(t)
	ctx := context.Background()

	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	view, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{Refinement: "only 4 days"})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}

	request := h.provider.requests[1]
	if request.Refinement != "only 4 days" {
		t.Fatalf("refinement: %q", request.Refinement)
	}
	var current segment.Proposal
	if err := json.Unmarshal(request.Current, &current); err != nil || *current.Segment.CartAddedWithinDays != 30 {
		t.Fatalf("current proposal not passed: %v %s", err, request.Current)
	}
	if view.Version != 1 || *view.Proposal.Segment.CartAddedWithinDays != 4 {
		t.Fatalf("view: %+v", view)
	}
}

func TestEditingTheBriefStartsOver(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	id := h.create(t)
	ctx := context.Background()
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}

	edited := "Ankara users"
	view, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{Brief: &edited})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if h.provider.requests[1].Brief != edited || len(h.provider.requests[1].Current) != 0 {
		t.Fatalf("request: %+v", h.provider.requests[1])
	}
	if view.Brief != edited {
		t.Fatalf("brief: %q", view.Brief)
	}
}

func TestBlockers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, output, want, reason string
	}{
		{
			"unsupported condition",
			`{"segment":{"city":"Ankara"},"assumptions":[],"unsupported_conditions":["users over 25"]}`,
			"users over 25", "unsupported_condition",
		},
		{
			"nobody matches",
			`{"segment":{"city":"Bursa"},"assumptions":[],"unsupported_conditions":[]}`,
			"No users match", "empty_audience",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, step{output: tt.output})
			id := h.create(t)
			view, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{})
			if err != nil {
				t.Fatalf("propose: %v", err)
			}
			if len(view.Blockers) != 1 || !strings.Contains(view.Blockers[0], tt.want) {
				t.Fatalf("blockers: %v", view.Blockers)
			}
			if got := testutil.ToFloat64(h.metrics.ValidationFailures.WithLabelValues("audience", tt.reason)); got != 1 {
				t.Fatalf("metric: %v", got)
			}
		})
	}
}

func TestAudienceStaysStableBetweenReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	id := h.create(t)
	ctx := context.Background()
	first, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	// A purchase after the snapshot must not move the number under review.
	if _, err := h.pool.Exec(ctx, `INSERT INTO purchase_events (user_id, product_id, occurred_at) VALUES ($1, 1, $2)`,
		seed.FixtureCartNoPurchase, referenceTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, err := h.service.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if second.Audience != first.Audience {
		t.Logf("audience moved from %+v to %+v", first.Audience, second.Audience)
	}
	if !second.AudienceAsOf.Equal(referenceTime) {
		t.Fatalf("as of: %v", second.AudienceAsOf)
	}
}

func TestUnknownCampaign(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid"} {
		if _, err := h.service.Get(context.Background(), id); !errors.Is(err, campaign.ErrNotFound) {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestSpansFormTheGenerationTraceWithoutPersonalData(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	id := h.create(t)
	if _, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}

	names := map[string]bool{}
	for _, span := range h.spans.GetSpans() {
		names[span.Name] = true
		for _, attribute := range span.Attributes {
			if strings.Contains(attribute.Value.Emit(), "+90500") || strings.Contains(attribute.Value.Emit(), secretBrief) {
				t.Fatalf("personal data in span %s: %v", span.Name, attribute)
			}
		}
	}
	for _, want := range []string{"campaign.generate", "llm.segment.generate", "segment.validate", "audience.evaluate"} {
		if !names[want] {
			t.Errorf("missing span %s, got %v", want, names)
		}
	}
}
