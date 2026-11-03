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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

const (
	// MaxInstructionLength limits regeneration instructions, in characters.
	MaxInstructionLength = 500

	// EventDelta carries text for one field; EventReset discards what was shown.
	EventDelta = "delta"
	EventReset = "reset"

	statusGenerating = "GENERATING"
	statusReady      = "READY_FOR_REVIEW"
	statusFailed     = "GENERATION_FAILED"

	operationContent  = "content_generation"
	stageContent      = "content"
	saveTimeout       = 10 * time.Second
	eventGenerated    = "content.generated"
	eventEdited       = "content.edited"
	eventGenFailed    = "content.generation_failed"
	outcomeCancelled  = "cancelled"
	abandonedInterval = "5 minutes"
	// abandonedAfter must outlast every attempt of one generation, so a running
	// generation is never taken over.
	abandonedAfter = 5 * time.Minute
)

// Errors the API maps to responses.
var (
	ErrNoAudience         = errors.New("campaign has no audience yet")
	ErrAudienceBlocked    = errors.New("audience has unresolved problems")
	ErrGenerationRunning  = errors.New("content is already being generated")
	ErrContentRejected    = errors.New("content failed validation")
	ErrInvalidInstruction = errors.New("instruction is too long")
)

// freeToDraft is the SQL condition for a campaign nothing else is generating
// for: a draft stage, or a generation whose process died without finishing.
const freeToDraft = `(status IN ('DRAFT', 'READY_FOR_REVIEW', 'GENERATION_FAILED')
	OR (status = 'GENERATING' AND updated_at < now() - interval '` + abandonedInterval + `'))`

// StoredContent is the campaign's messages and which of them a person changed.
type StoredContent struct {
	content.Content
	Edited []string `json:"edited"`
}

// GenerateInput steers a generation. Instruction is optional.
type GenerateInput struct {
	Instruction string
}

// GenerationEvent is a piece of progress. The text is for display only: nothing
// counts as content until the complete output has passed validation.
type GenerationEvent struct {
	Type  string
	Field string
	Text  string
}

// Generation is a claimed generation, ready to run.
type Generation struct {
	service     *Service
	view        View
	instruction string
}

// StartGeneration checks the campaign can get content and claims it, so that
// failures here can still be reported as an ordinary response.
func (s *Service) StartGeneration(ctx context.Context, id string, input GenerateInput) (*Generation, error) {
	instruction := strings.TrimSpace(input.Instruction)
	if utf8.RuneCountInString(instruction) > MaxInstructionLength {
		return nil, ErrInvalidInstruction
	}
	view, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch view.Status {
	case statusDraft, statusReady, statusFailed, statusGenerating:
	default:
		return nil, ErrNotEditable
	}
	if view.Proposal == nil {
		return nil, ErrNoAudience
	}
	if len(view.Blockers) > 0 {
		return nil, ErrAudienceBlocked
	}
	result, err := s.db.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1 AND `+freeToDraft, id, statusGenerating)
	if err != nil {
		return nil, fmt.Errorf("claim generation: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil, ErrGenerationRunning
	}
	return &Generation{service: s, view: view, instruction: instruction}, nil
}

// Run writes the messages, repairing invalid output a bounded number of times.
// Output that never validates is not stored, and existing content survives a
// failure. Cancelling ctx means someone stopped it, which is not a failure.
func (g *Generation) Run(ctx context.Context, emit func(GenerationEvent)) (view View, err error) {
	s := g.service
	ctx, span := s.tracer.Start(ctx, "campaign.generate", trace.WithAttributes(attribute.String("campaign.id", g.view.ID)))
	defer func() {
		if err != nil && ctx.Err() == nil {
			span.SetStatus(codes.Error, "generation failed")
		}
		span.End()
	}()

	generated, attempts, usage, err := g.generate(ctx, emit)
	// The outcome is recorded even when the client has just gone away.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	if err != nil {
		if recordErr := g.recordFailure(saveCtx, err, ctx.Err() != nil); recordErr != nil {
			slog.ErrorContext(saveCtx, "record generation failure", "error", recordErr)
		}
		if ctx.Err() != nil {
			return View{}, ctx.Err()
		}
		return View{}, err
	}
	if err := g.save(saveCtx, generated, attempts, usage); err != nil {
		return View{}, err
	}
	return s.Get(saveCtx, g.view.ID)
}

func (g *Generation) generate(ctx context.Context, emit func(GenerationEvent)) (content.Content, int, ai.Usage, error) {
	s := g.service
	request, err := g.request()
	if err != nil {
		return content.Content{}, 0, ai.Usage{}, err
	}
	offerSource := content.OfferSource(g.view.Brief, g.instruction)

	var (
		lastInvalid *content.ValidationError
		usage       ai.Usage
	)
	for attempt := 1; attempt <= MaxProposalAttempts; attempt++ {
		if attempt > 1 {
			emit(GenerationEvent{Type: EventReset})
		}
		response, err := g.callProvider(ctx, request, attempt, emit)
		if err != nil {
			return content.Content{}, attempt, usage, err
		}
		usage.InputTokens += response.Usage.InputTokens
		usage.OutputTokens += response.Usage.OutputTokens

		generated, err := g.validate(ctx, response.Output, offerSource)
		if err == nil {
			s.metrics.LLMRequests.WithLabelValues(operationContent, s.provider.Name(), outcomeSuccess).Inc()
			return generated, attempt, usage, nil
		}
		if !errors.As(err, &lastInvalid) {
			return content.Content{}, attempt, usage, err
		}
		s.metrics.LLMRequests.WithLabelValues(operationContent, s.provider.Name(), outcomeInvalid).Inc()
		reasons := issueReasons(lastInvalid)
		for _, reason := range reasons {
			s.metrics.ValidationFailures.WithLabelValues(stageContent, reason).Inc()
		}
		slog.WarnContext(ctx, "content rejected", "attempt", attempt, "reasons", reasons)
		request.PreviousOutput = string(response.Output)
		request.ValidationError = lastInvalid.Error()
	}
	return content.Content{}, MaxProposalAttempts, usage, fmt.Errorf("%w: %w", ErrContentRejected, lastInvalid)
}

func (g *Generation) request() (ai.ContentRequest, error) {
	request := ai.ContentRequest{
		Brief:       g.view.Brief,
		Schema:      content.JSONSchema(g.view.Channels...),
		Limits:      g.view.ContentLimits,
		Instruction: g.instruction,
	}
	for _, condition := range g.view.Conditions {
		request.Conditions = append(request.Conditions, condition.Label+" "+condition.Value)
	}
	if g.view.Content != nil {
		current, err := json.Marshal(g.view.Content.Content)
		if err != nil {
			return ai.ContentRequest{}, fmt.Errorf("encode current content: %w", err)
		}
		request.Current = current
	}
	return request, nil
}

func (g *Generation) callProvider(ctx context.Context, request ai.ContentRequest, attempt int, emit func(GenerationEvent)) (ai.Response, error) {
	s := g.service
	name := s.provider.Name()
	callCtx, cancel := context.WithTimeout(ctx, llmCallTimeout)
	defer cancel()
	callCtx, span := s.tracer.Start(callCtx, "llm.content.generate", trace.WithAttributes(
		attribute.String("llm.provider", name),
		attribute.String("llm.model", s.provider.Model()),
		attribute.Int("llm.attempt", attempt),
	))
	defer span.End()

	start := time.Now()
	firstText := true
	newStream := func() *content.FieldStream {
		return content.NewFieldStream(func(field, text string) {
			emit(GenerationEvent{Type: EventDelta, Field: field, Text: text})
		})
	}
	stream := newStream()
	response, err := s.provider.StreamContent(callCtx, request, ai.StreamHandler{
		OnText: func(text string) {
			if firstText {
				firstText = false
				s.metrics.LLMTTFT.WithLabelValues(operationContent, name).Observe(time.Since(start).Seconds())
			}
			stream.Write(text)
		},
		OnRestart: func() {
			stream = newStream()
			emit(GenerationEvent{Type: EventReset})
		},
	})
	s.metrics.LLMDuration.WithLabelValues(operationContent, name).Observe(time.Since(start).Seconds())
	if err != nil {
		outcome := providerOutcome(err)
		if ctx.Err() != nil {
			outcome = outcomeCancelled
		}
		s.metrics.LLMRequests.WithLabelValues(operationContent, name, outcome).Inc()
		span.SetStatus(codes.Error, outcome)
		slog.WarnContext(ctx, "llm call failed", "provider", name, "outcome", outcome, "error", err)
		return ai.Response{}, err
	}
	s.metrics.LLMTokens.WithLabelValues(operationContent, name, "input").Add(float64(response.Usage.InputTokens))
	s.metrics.LLMTokens.WithLabelValues(operationContent, name, "output").Add(float64(response.Usage.OutputTokens))
	span.SetAttributes(
		attribute.Int64("llm.tokens.input", response.Usage.InputTokens),
		attribute.Int64("llm.tokens.output", response.Usage.OutputTokens),
	)
	return response, nil
}

func (g *Generation) validate(ctx context.Context, output []byte, offerSource string) (content.Content, error) {
	_, span := g.service.tracer.Start(ctx, "content.validate")
	defer span.End()
	generated, err := content.ParseGenerated(output, g.view.ContentLimits, offerSource)
	if err == nil {
		if issues := content.CheckLinks(generated, g.linkSource()); len(issues) > 0 {
			err = &content.ValidationError{Issues: issues}
		}
	}
	var invalid *content.ValidationError
	if errors.As(err, &invalid) {
		span.SetStatus(codes.Error, "rejected")
		span.SetAttributes(attribute.StringSlice("validation.reasons", issueReasons(invalid)))
	}
	return generated, err
}

// linkSource is everything a person wrote that a generated link may come from,
// including a link they already set on the current version.
func (g *Generation) linkSource() string {
	source := g.view.Brief + "\n" + g.instruction
	if g.view.Content != nil {
		source += "\n" + g.view.Content.EmailCTAURL
	}
	return source
}

func issueReasons(invalid *content.ValidationError) []string {
	reasons := make([]string, 0, len(invalid.Issues))
	for _, issue := range invalid.Issues {
		reasons = append(reasons, issue.Reason)
	}
	return reasons
}

func (g *Generation) save(ctx context.Context, generated content.Content, attempts int, usage ai.Usage) error {
	s := g.service
	encoded, err := json.Marshal(StoredContent{Content: generated, Edited: []string{}})
	if err != nil {
		return fmt.Errorf("encode content: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin content save: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	result, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1 AND status = $3`, g.view.ID, statusReady, statusGenerating)
	if err != nil {
		return fmt.Errorf("finish generation: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrGenerationRunning
	}
	if _, err := tx.Exec(ctx, `UPDATE campaign_versions SET content = $3, llm_provider = $4, llm_model = $5 WHERE campaign_id = $1 AND version = $2`,
		g.view.ID, g.view.Version, encoded, s.provider.Name(), s.provider.Model()); err != nil {
		return fmt.Errorf("save content: %w", err)
	}
	if err := recordAudit(ctx, tx, g.view.ID, eventGenerated, map[string]any{
		"provider": s.provider.Name(), "model": s.provider.Model(), "attempts": attempts,
		"regenerated": g.view.Content != nil, "instruction_given": g.instruction != "",
		"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit content: %w", err)
	}
	return nil
}

// recordFailure releases the claim. Existing content stays reviewable; a campaign
// someone stopped goes back to where it was without counting as a failure.
func (g *Generation) recordFailure(ctx context.Context, failure error, cancelled bool) error {
	s := g.service
	status := statusDraft
	switch {
	case g.view.Content != nil:
		status = statusReady
	case !cancelled:
		status = statusFailed
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failure record: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1 AND status = $3`, g.view.ID, status, statusGenerating); err != nil {
		return fmt.Errorf("release generation: %w", err)
	}
	if !cancelled {
		reason := providerOutcome(failure)
		if errors.Is(failure, ErrContentRejected) {
			reason = outcomeInvalid
		}
		if err := recordAudit(ctx, tx, g.view.ID, eventGenFailed, map[string]any{"reason": reason}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SaveContent stores a person's edits. They pass the same hard limits as
// generated text, but a discount that differs from the brief is only pointed out:
// the marketer may change the offer, the model may not.
func (s *Service) SaveContent(ctx context.Context, id string, edited content.Content) (View, error) {
	view, err := s.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if view.Status != statusReady || view.Content == nil {
		return View{}, ErrNotEditable
	}
	edited = view.ContentLimits.Keep(edited)
	if issues := content.CheckMessages(edited, view.ContentLimits); len(issues) > 0 {
		return View{}, &content.ValidationError{Issues: issues}
	}
	changed := changedFields(view.Content.Content, edited)
	if len(changed) == 0 {
		return view, nil
	}
	encoded, err := json.Marshal(StoredContent{Content: edited, Edited: mergeEdited(view.Content.Edited, changed)})
	if err != nil {
		return View{}, fmt.Errorf("encode content: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin content edit: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	result, err := tx.Exec(ctx, `UPDATE campaigns SET updated_at = now() WHERE id = $1 AND status = $2`, id, statusReady)
	if err != nil {
		return View{}, fmt.Errorf("touch campaign: %w", err)
	}
	if result.RowsAffected() == 0 {
		return View{}, ErrNotEditable
	}
	if _, err := tx.Exec(ctx, `UPDATE campaign_versions SET content = $3 WHERE campaign_id = $1 AND version = $2`, id, view.Version, encoded); err != nil {
		return View{}, fmt.Errorf("save edited content: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventEdited, map[string]any{"fields": changed}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit content edit: %w", err)
	}
	return s.Get(ctx, id)
}

func changedFields(before, after content.Content) []string {
	changed := []string{}
	for _, field := range []struct {
		name          string
		before, after string
	}{
		{content.FieldSMS, before.SMS, after.SMS},
		{content.FieldEmailSubject, before.EmailSubject, after.EmailSubject},
		{content.FieldEmailPreheader, before.EmailPreheader, after.EmailPreheader},
		{content.FieldEmailHeadline, before.EmailHeadline, after.EmailHeadline},
		{content.FieldEmailBody, before.EmailBody, after.EmailBody},
		{content.FieldEmailCTALabel, before.EmailCTALabel, after.EmailCTALabel},
		{content.FieldEmailCTAURL, before.EmailCTAURL, after.EmailCTAURL},
	} {
		if field.before != field.after {
			changed = append(changed, field.name)
		}
	}
	return changed
}

func mergeEdited(existing, changed []string) []string {
	merged := []string{}
	for _, field := range []string{content.FieldSMS, content.FieldEmailSubject, content.FieldEmailPreheader, content.FieldEmailHeadline, content.FieldEmailBody, content.FieldEmailCTALabel, content.FieldEmailCTAURL} {
		for _, list := range [][]string{existing, changed} {
			if contains(list, field) {
				merged = append(merged, field)
				break
			}
		}
	}
	return merged
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// SampleRecipients picks audience members for previews, edge cases first.
func (s *Service) SampleRecipients(ctx context.Context, id string) ([]segment.Sample, error) {
	view, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if view.Proposal == nil {
		return nil, ErrNoAudience
	}
	return segment.SampleRecipients(ctx, s.db, view.Proposal.Segment, view.AudienceAsOf)
}

// generationAbandoned says whether a GENERATING status outlived its process.
func generationAbandoned(view View) bool {
	return view.Status == statusGenerating && time.Since(view.UpdatedAt) >= abandonedAfter
}
