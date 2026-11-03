// Package campaign owns the campaign lifecycle: it asks a provider for a
// proposal, passes it through validation and records what was decided.
package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

const (
	// MaxBriefLength limits briefs, in characters.
	MaxBriefLength = 2000
	// MaxProposalAttempts is the first try plus repairs of invalid output.
	MaxProposalAttempts = 3

	statusDraft        = "DRAFT"
	operationSegment   = "segment_generation"
	llmCallTimeout     = 90 * time.Second
	stageSegment       = "segment"
	stageAudience      = "audience"
	outcomeSuccess     = "success"
	outcomeInvalid     = "invalid_output"
	reasonUnsupported  = "unsupported_condition"
	reasonEmpty        = "empty_audience"
	reasonNoConsent    = "no_consented_recipients"
	eventCreated       = "campaign.created"
	eventSegmentChosen = "segment.proposed"
)

// Errors the API maps to responses.
var (
	ErrInvalidBrief     = errors.New("brief is empty or too long")
	ErrNotFound         = errors.New("campaign not found")
	ErrNotEditable      = errors.New("campaign can no longer be edited")
	ErrProposalRejected = errors.New("proposal failed validation")
)

// ProposeInput changes what is proposed. Brief replaces the brief and starts
// over; Refinement adjusts the current proposal.
type ProposeInput struct {
	Brief      *string
	Refinement string
}

// View is a campaign as the marketer sees it.
type View struct {
	ID           string              `json:"id"`
	Brief        string              `json:"brief"`
	Status       string              `json:"status"`
	Version      int                 `json:"version"`
	Provider     string              `json:"provider,omitempty"`
	Model        string              `json:"model,omitempty"`
	Proposal     *segment.Proposal   `json:"proposal,omitempty"`
	Conditions   []segment.Condition `json:"conditions"`
	Audience     segment.Counts      `json:"audience"`
	AudienceAsOf time.Time           `json:"audience_as_of,omitzero"`
	Blockers     []string            `json:"blockers"`
}

// Service runs campaign operations.
type Service struct {
	db       *pgxpool.Pool
	provider ai.Provider
	metrics  *telemetry.Metrics
	tracer   trace.Tracer
	now      func() time.Time
}

// NewService builds a service.
func NewService(db *pgxpool.Pool, provider ai.Provider, metrics *telemetry.Metrics, tracer trace.Tracer, now func() time.Time) *Service {
	return &Service{db: db, provider: provider, metrics: metrics, tracer: tracer, now: now}
}

// Create stores a new draft and returns its ID.
func (s *Service) Create(ctx context.Context, brief string) (string, error) {
	brief, err := cleanBrief(brief)
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin create: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO campaigns (brief) VALUES ($1) RETURNING id::text`, brief).Scan(&id); err != nil {
		return "", fmt.Errorf("insert campaign: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventCreated, map[string]any{"brief_characters": utf8.RuneCountInString(brief)}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit create: %w", err)
	}
	return id, nil
}

func cleanBrief(brief string) (string, error) {
	brief = strings.TrimSpace(brief)
	if brief == "" || utf8.RuneCountInString(brief) > MaxBriefLength {
		return "", ErrInvalidBrief
	}
	return brief, nil
}

// ProposeSegment asks the provider for an audience, repairs invalid output a
// bounded number of times and stores the result as the draft version. Output
// that never validates is not stored.
func (s *Service) ProposeSegment(ctx context.Context, id string, input ProposeInput) (view View, err error) {
	ctx, span := s.tracer.Start(ctx, "campaign.generate", trace.WithAttributes(attribute.String("campaign.id", id)))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "generation failed")
		}
		span.End()
	}()

	stored, err := s.load(ctx, id)
	if err != nil {
		return View{}, err
	}
	if stored.Status != statusDraft {
		return View{}, ErrNotEditable
	}

	request := ai.SegmentRequest{Brief: stored.Brief, Schema: segment.JSONSchema(), Refinement: input.Refinement}
	if input.Brief != nil {
		if request.Brief, err = cleanBrief(*input.Brief); err != nil {
			return View{}, err
		}
		request.Refinement = ""
	} else if input.Refinement != "" && stored.Proposal != nil {
		if request.Current, err = json.Marshal(stored.Proposal); err != nil {
			return View{}, fmt.Errorf("encode current proposal: %w", err)
		}
	}
	if request.Cities, err = s.cities(ctx); err != nil {
		return View{}, err
	}

	proposal, attempts, err := s.generateProposal(ctx, request)
	if err != nil {
		return View{}, err
	}

	asOf := s.now()
	counts, err := s.evaluateAudience(ctx, proposal.Segment, asOf)
	if err != nil {
		return View{}, err
	}
	for _, blocker := range blockersFor(proposal, counts) {
		s.metrics.ValidationFailures.WithLabelValues(stageAudience, blocker.reason).Inc()
	}
	if err := s.saveProposal(ctx, id, request.Brief, proposal, counts, asOf, attempts); err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) generateProposal(ctx context.Context, request ai.SegmentRequest) (segment.Proposal, int, error) {
	var lastInvalid *segment.ValidationError
	for attempt := 1; attempt <= MaxProposalAttempts; attempt++ {
		response, err := s.callProvider(ctx, request, attempt)
		if err != nil {
			return segment.Proposal{}, attempt, err
		}
		proposal, err := s.validateProposal(ctx, response.Output, request.Cities)
		if err == nil {
			s.metrics.LLMRequests.WithLabelValues(operationSegment, s.provider.Name(), outcomeSuccess).Inc()
			return proposal, attempt, nil
		}
		if !errors.As(err, &lastInvalid) {
			return segment.Proposal{}, attempt, err
		}
		s.metrics.LLMRequests.WithLabelValues(operationSegment, s.provider.Name(), outcomeInvalid).Inc()
		for _, reason := range lastInvalid.Reasons {
			s.metrics.ValidationFailures.WithLabelValues(stageSegment, reason).Inc()
		}
		slog.WarnContext(ctx, "proposal rejected", "attempt", attempt, "reasons", lastInvalid.Reasons)
		request.PreviousOutput = string(response.Output)
		request.ValidationError = lastInvalid.Error()
	}
	return segment.Proposal{}, MaxProposalAttempts, fmt.Errorf("%w: %w", ErrProposalRejected, lastInvalid)
}

func (s *Service) callProvider(ctx context.Context, request ai.SegmentRequest, attempt int) (ai.SegmentResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, llmCallTimeout)
	defer cancel()
	ctx, span := s.tracer.Start(ctx, "llm.segment.generate", trace.WithAttributes(
		attribute.String("llm.provider", s.provider.Name()),
		attribute.String("llm.model", s.provider.Model()),
		attribute.Int("llm.attempt", attempt),
	))
	defer span.End()

	start := time.Now()
	response, err := s.provider.ProposeSegment(ctx, request)
	name := s.provider.Name()
	s.metrics.LLMDuration.WithLabelValues(operationSegment, name).Observe(time.Since(start).Seconds())
	if err != nil {
		outcome := providerOutcome(err)
		s.metrics.LLMRequests.WithLabelValues(operationSegment, name, outcome).Inc()
		span.SetStatus(codes.Error, outcome)
		slog.WarnContext(ctx, "llm call failed", "provider", name, "outcome", outcome, "error", err)
		return ai.SegmentResponse{}, err
	}
	s.metrics.LLMTokens.WithLabelValues(operationSegment, name, "input").Add(float64(response.Usage.InputTokens))
	s.metrics.LLMTokens.WithLabelValues(operationSegment, name, "output").Add(float64(response.Usage.OutputTokens))
	span.SetAttributes(
		attribute.Int64("llm.tokens.input", response.Usage.InputTokens),
		attribute.Int64("llm.tokens.output", response.Usage.OutputTokens),
	)
	return response, nil
}

func providerOutcome(err error) string {
	switch {
	case errors.Is(err, ai.ErrRefused):
		return "refused"
	case errors.Is(err, ai.ErrTruncated):
		return "truncated"
	case errors.Is(err, ai.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ai.ErrInvalidOutput):
		return outcomeInvalid
	default:
		return "unavailable"
	}
}

func (s *Service) validateProposal(ctx context.Context, output []byte, cities []string) (segment.Proposal, error) {
	_, span := s.tracer.Start(ctx, "segment.validate")
	defer span.End()
	proposal, err := segment.ParseProposal(output, cities)
	var invalid *segment.ValidationError
	if errors.As(err, &invalid) {
		span.SetStatus(codes.Error, "rejected")
		span.SetAttributes(attribute.StringSlice("validation.reasons", invalid.Reasons))
	}
	return proposal, err
}

func (s *Service) evaluateAudience(ctx context.Context, spec segment.Spec, asOf time.Time) (segment.Counts, error) {
	ctx, span := s.tracer.Start(ctx, "audience.evaluate")
	defer span.End()
	counts, err := segment.Count(ctx, s.db, spec, asOf)
	if err != nil {
		span.SetStatus(codes.Error, "count failed")
		return segment.Counts{}, err
	}
	span.SetAttributes(attribute.Int("audience.total", counts.Total))
	return counts, nil
}

// cities is the allowlist a proposal's city is checked against: whatever exists
// in user data, so the model cannot invent one.
func (s *Service) cities(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT city FROM users ORDER BY city`)
	if err != nil {
		return nil, fmt.Errorf("list cities: %w", err)
	}
	cities, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("list cities: %w", err)
	}
	return cities, nil
}

func (s *Service) saveProposal(ctx context.Context, id, brief string, proposal segment.Proposal, counts segment.Counts, asOf time.Time, attempts int) error {
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return fmt.Errorf("encode proposal: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if _, err := tx.Exec(ctx, `UPDATE campaigns SET brief = $2, updated_at = now() WHERE id = $1`, id, brief); err != nil {
		return fmt.Errorf("update campaign: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO campaign_versions (campaign_id, version, segment, content, llm_provider, llm_model, audience_as_of)
		VALUES ($1, 1, $2, '{}', $3, $4, $5)
		ON CONFLICT (campaign_id, version) DO UPDATE
		SET segment = EXCLUDED.segment, llm_provider = EXCLUDED.llm_provider,
		    llm_model = EXCLUDED.llm_model, audience_as_of = EXCLUDED.audience_as_of`,
		id, encoded, s.provider.Name(), s.provider.Model(), asOf); err != nil {
		return fmt.Errorf("save version: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventSegmentChosen, map[string]any{
		"provider": s.provider.Name(), "model": s.provider.Model(), "attempts": attempts,
		"audience": counts, "assumptions": len(proposal.Assumptions),
		"unsupported_conditions": len(proposal.UnsupportedConditions),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit save: %w", err)
	}
	return nil
}

func recordAudit(ctx context.Context, tx pgx.Tx, id, event string, details map[string]any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (campaign_id, event_type, details) VALUES ($1, $2, $3)`, id, event, encoded); err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	return nil
}

// Get returns a campaign with its audience, counted at the time the proposal was
// made so the size under review does not drift as time windows slide.
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	view, err := s.load(ctx, id)
	if err != nil {
		return View{}, err
	}
	if view.Proposal == nil {
		return view, nil
	}
	view.Conditions = segment.Describe(view.Proposal.Segment)
	if view.Audience, err = segment.Count(ctx, s.db, view.Proposal.Segment, view.AudienceAsOf); err != nil {
		return View{}, err
	}
	for _, blocker := range blockersFor(*view.Proposal, view.Audience) {
		view.Blockers = append(view.Blockers, blocker.message)
	}
	return view, nil
}

func (s *Service) load(ctx context.Context, id string) (View, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return View{}, ErrNotFound
	}
	view := View{ID: id, Conditions: []segment.Condition{}, Blockers: []string{}}
	var (
		version      *int
		provider     *string
		model        *string
		encoded      []byte
		audienceAsOf *time.Time
	)
	err := s.db.QueryRow(ctx, `
		SELECT c.brief, c.status, v.version, v.llm_provider, v.llm_model, v.segment, v.audience_as_of
		FROM campaigns c
		LEFT JOIN LATERAL (
			SELECT * FROM campaign_versions WHERE campaign_id = c.id ORDER BY version DESC LIMIT 1
		) v ON true
		WHERE c.id = $1`, parsed).Scan(&view.Brief, &view.Status, &version, &provider, &model, &encoded, &audienceAsOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("load campaign: %w", err)
	}
	if version == nil {
		return view, nil
	}
	view.Version = *version
	if provider != nil {
		view.Provider = *provider
	}
	if model != nil {
		view.Model = *model
	}
	if audienceAsOf != nil {
		view.AudienceAsOf = *audienceAsOf
	}
	view.Proposal = &segment.Proposal{}
	if err := json.Unmarshal(encoded, view.Proposal); err != nil {
		return View{}, fmt.Errorf("decode stored proposal: %w", err)
	}
	return view, nil
}

type blocker struct {
	reason  string
	message string
}

// blockersFor lists what must change before this audience can be approved.
func blockersFor(proposal segment.Proposal, counts segment.Counts) []blocker {
	blockers := []blocker{}
	for _, condition := range proposal.UnsupportedConditions {
		blockers = append(blockers, blocker{reasonUnsupported, fmt.Sprintf(
			"The brief asks for %q, which no audience filter can express. Refine the audience to remove it.", condition)})
	}
	switch {
	case counts.Total == 0:
		blockers = append(blockers, blocker{reasonEmpty, "No users match these conditions. Refine the audience to widen it."})
	case counts.SMS == 0 && counts.Email == 0:
		blockers = append(blockers, blocker{reasonNoConsent, "None of the matching users have consented to SMS or email."})
	}
	return blockers
}
