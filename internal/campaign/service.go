// Package campaign owns the campaign lifecycle: it asks a provider for a
// proposal, passes it through validation and records what was decided.
package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
	"github.com/negativexq/smart-campaign-composer/internal/content"
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
	eventRenamed       = "campaign.renamed"
	maxProvisionalName = 60
	eventCreated       = "campaign.created"
	eventSegmentChosen = "segment.proposed"
)

// Errors the API maps to responses.
var (
	ErrInvalidBrief     = errors.New("brief is empty or too long")
	ErrInvalidChannels  = errors.New("channels must be sms, email or both")
	ErrInvalidOptions   = errors.New("tone, offer or exclusion is not valid")
	ErrOfferConflict    = errors.New("brief mentions a different discount than the offer")
	ErrInvalidName      = errors.New("campaign name is empty or too long")
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
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	NameSource   string   `json:"name_source"`
	Brief        string   `json:"brief"`
	Status       string   `json:"status"`
	Channels     []string `json:"channels"`
	Tone         string   `json:"tone"`
	OfferPercent *int     `json:"offer_percent"`
	// NotMessagedWithinDays is what the form asked for; the audience rule that
	// applies is in Proposal.
	NotMessagedWithinDays *int                `json:"not_messaged_within_days"`
	CreatedBy             *string             `json:"created_by"`
	Version               int                 `json:"version"`
	Provider              string              `json:"provider,omitempty"`
	Model                 string              `json:"model,omitempty"`
	Proposal              *segment.Proposal   `json:"proposal,omitempty"`
	Conditions            []segment.Condition `json:"conditions"`
	Audience              segment.Counts      `json:"audience"`
	AudienceAsOf          time.Time           `json:"audience_as_of,omitzero"`
	Blockers              []string            `json:"blockers"`
	Content               *StoredContent      `json:"content,omitempty"`
	// ContentLimits is how long {{first_name}} can get in this audience.
	ContentLimits content.Limits `json:"content_limits"`
	// ContentWarnings are differences from the brief for a person to judge.
	ContentWarnings []content.Issue `json:"content_warnings"`
	ApprovedAt      time.Time       `json:"approved_at,omitzero"`
	Schedule        *ScheduleView   `json:"schedule,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
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
func (s *Service) Create(ctx context.Context, brief string, chosen ...string) (string, error) {
	return s.CreateWith(ctx, NewCampaign{Brief: brief, Channels: chosen})
}

// NewCampaign is what a marketer fills in. Only the brief is required.
type NewCampaign struct {
	Brief                 string
	Channels              []string
	Tone                  string
	OfferPercent          *int
	NotMessagedWithinDays *int
}

func (n NewCampaign) validate() error {
	if !slices.Contains([]string{"", "friendly", "formal", "urgent"}, n.Tone) {
		return ErrInvalidOptions
	}
	if n.OfferPercent != nil && (*n.OfferPercent < 1 || *n.OfferPercent > 100) {
		return ErrInvalidOptions
	}
	if n.NotMessagedWithinDays != nil && (*n.NotMessagedWithinDays < 1 || *n.NotMessagedWithinDays > 365) {
		return ErrInvalidOptions
	}
	return checkOffer(n.Brief, n.OfferPercent)
}

// checkOffer refuses two different offers rather than guessing which one is meant.
func checkOffer(brief string, offer *int) error {
	if offer != nil && !content.OfferAgrees(brief, *offer) {
		return ErrOfferConflict
	}
	return nil
}

// CreateWith stores a new draft with the choices made on the form.
func (s *Service) CreateWith(ctx context.Context, campaign NewCampaign) (string, error) {
	brief, err := cleanBrief(campaign.Brief)
	if err != nil {
		return "", err
	}
	chosen, err := normalizeChannels(campaign.Channels)
	if err != nil {
		return "", err
	}
	if err := campaign.validate(); err != nil {
		return "", err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin create: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO campaigns (brief, name, created_by, channels, tone, offer_percent, not_messaged_within_days)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id::text`,
		brief, provisionalName(brief), personFrom(ctx), chosen, campaign.Tone, campaign.OfferPercent, campaign.NotMessagedWithinDays).Scan(&id); err != nil {
		return "", fmt.Errorf("insert campaign: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventCreated, map[string]any{"brief_characters": utf8.RuneCountInString(brief), "channels": chosen, "tone": campaign.Tone, "offer_percent": campaign.OfferPercent, "not_messaged_within_days": campaign.NotMessagedWithinDays}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit create: %w", err)
	}
	return id, nil
}

// Rename sets the name someone chose. Names label a campaign rather than being
// part of the approved content, so this works in every status.
func (s *Service) Rename(ctx context.Context, id, name string) (View, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return View{}, ErrNotFound
	}
	name, ok := segment.CleanCampaignName(name)
	if !ok {
		return View{}, ErrInvalidName
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin rename: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	result, err := tx.Exec(ctx, `UPDATE campaigns SET name = $2, name_source = 'user', updated_at = now() WHERE id = $1`, parsed, name)
	if err != nil {
		return View{}, fmt.Errorf("rename campaign: %w", err)
	}
	if result.RowsAffected() == 0 {
		return View{}, ErrNotFound
	}
	if err := recordAudit(ctx, tx, id, eventRenamed, map[string]any{"name": name}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit rename: %w", err)
	}
	return s.Get(ctx, id)
}

// provisionalName labels a campaign until a better name exists, cutting the
// brief between words.
func provisionalName(brief string) string {
	words := strings.Fields(brief)
	name := strings.Join(words, " ")
	if utf8.RuneCountInString(name) <= maxProvisionalName {
		return name
	}
	var kept strings.Builder
	for _, word := range words {
		if utf8.RuneCountInString(kept.String())+utf8.RuneCountInString(word)+1 > maxProvisionalName-1 {
			break
		}
		if kept.Len() > 0 {
			kept.WriteByte(' ')
		}
		kept.WriteString(word)
	}
	if kept.Len() == 0 {
		kept.WriteString(string([]rune(name)[:maxProvisionalName-1]))
	}
	return kept.String() + "…"
}

// Unsubscribe withdraws a person's email consent. Deliveries still waiting read
// consent again before sending, so they stop as well.
func (s *Service) Unsubscribe(ctx context.Context, userID int64) error {
	if _, err := s.db.Exec(ctx, `UPDATE consents SET allowed = false, updated_at = now() WHERE user_id = $1 AND channel = 'email' AND allowed`, userID); err != nil {
		return fmt.Errorf("withdraw email consent: %w", err)
	}
	return nil
}

// writingBrief is the brief as a message writer and its checks see it, with the
// offer from the form stated like any other.
func (v View) writingBrief() string {
	if v.OfferPercent == nil {
		return v.Brief
	}
	return fmt.Sprintf("%s\n\nOffer set by the marketer: %d%% discount.", v.Brief, *v.OfferPercent)
}

// normalizeChannels puts a choice in the product's order. Saying nothing means
// both, while an empty list is a choice of none and is refused.
func normalizeChannels(chosen []string) ([]string, error) {
	if chosen == nil {
		return []string{"sms", "email"}, nil
	}
	normalized := make([]string, 0, 2)
	for _, channel := range []string{"sms", "email"} {
		if slices.Contains(chosen, channel) {
			normalized = append(normalized, channel)
		}
	}
	for _, channel := range chosen {
		if !slices.Contains(normalized, channel) {
			return nil, ErrInvalidChannels
		}
	}
	if len(normalized) == 0 {
		return nil, ErrInvalidChannels
	}
	return normalized, nil
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
	switch {
	case stored.Status == statusDraft, stored.Status == statusReady, stored.Status == statusFailed, generationAbandoned(stored):
	case stored.Status == statusGenerating:
		return View{}, ErrGenerationRunning
	default:
		return View{}, ErrNotEditable
	}

	request := ai.SegmentRequest{Brief: stored.Brief, Schema: segment.JSONSchema(), Refinement: input.Refinement}
	if input.Brief != nil {
		if request.Brief, err = cleanBrief(*input.Brief); err != nil {
			return View{}, err
		}
		if err := checkOffer(request.Brief, stored.OfferPercent); err != nil {
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
	// A refinement starts from the current rule, so a person can still ask to drop it.
	if request.Refinement == "" && stored.NotMessagedWithinDays != nil {
		proposal.Segment.NotMessagedWithinDays = stored.NotMessagedWithinDays
	}

	s.checkEvidence(&proposal, request, stored.Proposal)

	asOf := s.now()
	counts, err := s.evaluateAudience(ctx, proposal.Segment, asOf)
	if err != nil {
		return View{}, err
	}
	for _, blocker := range blockersFor(proposal, counts) {
		s.metrics.ValidationFailures.WithLabelValues(stageAudience, blocker.reason).Inc()
	}
	if err := s.saveProposal(ctx, id, request.Brief, proposal, counts, asOf, attempts, proposalTrigger(input)); err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

// checkEvidence keeps the quotes a person really wrote. A refinement builds on
// the current proposal, so quotes already checked for it still count.
func (s *Service) checkEvidence(proposal *segment.Proposal, request ai.SegmentRequest, current *segment.Proposal) {
	sources := []string{request.Brief, request.Refinement}
	if request.Refinement != "" && current != nil {
		for _, evidence := range current.Evidence {
			sources = append(sources, evidence.Quote)
		}
	}
	if dropped := proposal.KeepQuotedEvidence(sources...); dropped > 0 {
		s.metrics.ValidationFailures.WithLabelValues(stageSegment, "unquoted_evidence").Add(float64(dropped))
	}
}

// proposalTrigger says why a proposal was made, for the audit trail.
func proposalTrigger(input ProposeInput) string {
	switch {
	case input.Brief != nil:
		return "brief_edit"
	case input.Refinement != "":
		return "refinement"
	default:
		return "brief"
	}
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

func (s *Service) callProvider(ctx context.Context, request ai.SegmentRequest, attempt int) (ai.Response, error) {
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
		return ai.Response{}, err
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

func (s *Service) saveProposal(ctx context.Context, id, brief string, proposal segment.Proposal, counts segment.Counts, asOf time.Time, attempts int, trigger string) error {
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return fmt.Errorf("encode proposal: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// A name someone chose stays. Otherwise a suggestion wins, and a provisional
	// name follows the brief it came from. Content was written for the previous
	// audience, so the campaign goes back to a draft without any.
	result, err := tx.Exec(ctx, `
		UPDATE campaigns SET brief = $2, status = 'DRAFT', updated_at = now(),
			name = CASE WHEN name_source = 'user' THEN name
			            WHEN $3::text <> '' THEN $3::text
			            WHEN name_source = 'brief' THEN $4::text
			            ELSE name END,
			name_source = CASE WHEN name_source = 'user' THEN 'user'
			                   WHEN $3::text <> '' THEN 'ai'
			                   ELSE name_source END
		WHERE id = $1 AND `+freeToDraft, id, brief, proposal.CampaignName, provisionalName(brief))
	if err != nil {
		return fmt.Errorf("update campaign: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrGenerationRunning
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO campaign_versions (campaign_id, version, segment, content, llm_provider, llm_model, audience_as_of)
		VALUES ($1, 1, $2, '{}', $3, $4, $5)
		ON CONFLICT (campaign_id, version) DO UPDATE
		SET segment = EXCLUDED.segment, content = '{}', llm_provider = EXCLUDED.llm_provider,
		    llm_model = EXCLUDED.llm_model, audience_as_of = EXCLUDED.audience_as_of`,
		id, encoded, s.provider.Name(), s.provider.Model(), asOf); err != nil {
		return fmt.Errorf("save version: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventSegmentChosen, map[string]any{
		"trigger": trigger, "provider": s.provider.Name(), "model": s.provider.Model(), "attempts": attempts,
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
	actorType, actor := actorSystem, personFrom(ctx)
	if actor != nil {
		actorType = actorUser
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (campaign_id, event_type, details, actor_type, actor) VALUES ($1, $2, $3, $4, $5)`, id, event, encoded, actorType, actor); err != nil {
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
	if view.Schedule, err = s.loadSchedule(ctx, id); err != nil {
		return View{}, err
	}
	if view.Proposal == nil {
		return view, nil
	}
	view.Conditions = segment.Describe(view.Proposal.Segment)
	if view.Audience, err = segment.Count(ctx, s.db, view.Proposal.Segment, view.AudienceAsOf); err != nil {
		return View{}, err
	}
	// Reachable means on a channel this campaign uses.
	if !slices.Contains(view.Channels, "sms") {
		view.Audience.SMS = 0
	}
	if !slices.Contains(view.Channels, "email") {
		view.Audience.Email = 0
	}
	for _, blocker := range blockersFor(*view.Proposal, view.Audience) {
		view.Blockers = append(view.Blockers, blocker.message)
	}
	lengths, err := segment.LongestNames(ctx, s.db, view.Proposal.Segment, view.AudienceAsOf)
	if err != nil {
		return View{}, err
	}
	view.ContentLimits = content.Limits{
		SMSName: lengths.SMS, EmailName: lengths.Email,
		SkipSMS: !slices.Contains(view.Channels, "sms"), SkipEmail: !slices.Contains(view.Channels, "email"),
	}
	if view.Content != nil {
		view.ContentWarnings = append(view.ContentWarnings, content.CheckDiscounts(view.Content.Content, view.writingBrief())...)
	}
	return view, nil
}

func (s *Service) load(ctx context.Context, id string) (View, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return View{}, ErrNotFound
	}
	view := View{ID: id, Conditions: []segment.Condition{}, Blockers: []string{}, ContentWarnings: []content.Issue{}}
	var (
		version      *int
		provider     *string
		model        *string
		encoded      []byte
		storedText   []byte
		audienceAsOf *time.Time
		approvedAt   *time.Time
	)
	err := s.db.QueryRow(ctx, `
		SELECT c.name, c.name_source, c.brief, c.status, c.channels, c.tone, c.offer_percent, c.not_messaged_within_days, c.created_by, c.created_at, c.updated_at, v.version, v.llm_provider, v.llm_model, v.segment, v.content, v.audience_as_of, v.approved_at
		FROM campaigns c
		LEFT JOIN LATERAL (
			SELECT * FROM campaign_versions WHERE campaign_id = c.id ORDER BY version DESC LIMIT 1
		) v ON true
		WHERE c.id = $1`, parsed).Scan(&view.Name, &view.NameSource, &view.Brief, &view.Status, &view.Channels, &view.Tone, &view.OfferPercent, &view.NotMessagedWithinDays, &view.CreatedBy, &view.CreatedAt, &view.UpdatedAt, &version, &provider, &model, &encoded, &storedText, &audienceAsOf, &approvedAt)
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
	if approvedAt != nil {
		view.ApprovedAt = *approvedAt
	}
	view.Proposal = &segment.Proposal{}
	if err := json.Unmarshal(encoded, view.Proposal); err != nil {
		return View{}, fmt.Errorf("decode stored proposal: %w", err)
	}
	stored := StoredContent{}
	if err := json.Unmarshal(storedText, &stored); err != nil {
		return View{}, fmt.Errorf("decode stored content: %w", err)
	}
	if stored.Content != (content.Content{}) {
		view.Content = &stored
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
		blockers = append(blockers, blocker{reasonNoConsent, "None of the matching users have consented to the channels this campaign uses."})
	}
	return blockers
}
