package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
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
	// EventLanguage says which language is being written after the first one, whose
	// text is not shown as it comes because the screen shows the main version.
	EventLanguage = "language"
	// EventPlan lists, comma separated, every language this run will write, so a person
	// can see what is coming before any of it has started.
	EventPlan = "plan"
	// EventLanguageDone says one language's version is finished and has passed.
	EventLanguageDone = "language_done"
	// EventVariant says the second version of a test is being written.
	EventVariant = "variant"

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
	// abandonedAfter must outlast every attempt of one version, and each version says
	// the generation is alive, so a running generation is never taken over however
	// many versions it writes.
	abandonedAfter = 5 * time.Minute
)

// Errors the API maps to responses.
var (
	ErrNoAudience         = errors.New("campaign has no audience yet")
	ErrAudienceBlocked    = errors.New("audience has unresolved problems")
	ErrGenerationRunning  = errors.New("content is already being generated")
	ErrContentRejected    = errors.New("content failed validation")
	ErrInvalidInstruction = errors.New("instruction is too long")
	ErrChannelDiscount    = errors.New("channels would promise different discounts")
	ErrLanguageDiscount   = errors.New("languages or versions would promise different discounts")
	ErrMissingVariant     = errors.New("a test needs its second version written")
	ErrMissingLanguage    = errors.New("part of the audience reads a language with no messages")
	ErrContentChanged     = errors.New("the messages changed since they were read")
	ErrReviewChanged      = errors.New("the campaign changed since it was reviewed")
)

// freeToDraft is the SQL condition for a campaign nothing else is generating
// for: a draft stage, or a generation whose process died without finishing.
const freeToDraft = `(status IN ('DRAFT', 'READY_FOR_REVIEW', 'GENERATION_FAILED')
	OR (status = 'GENERATING' AND updated_at < now() - interval '` + abandonedInterval + `'))`

const reasonNotApplied = "not_applied"

// StoredContent is the campaign's messages and which of them a person changed.
type StoredContent struct {
	content.Content
	Edited []string `json:"edited"`
	// NotApplied holds one note per channel on what the last instruction asked for
	// and the messages do not do. Writing that channel again replaces its note.
	NotApplied []content.Issue `json:"not_applied,omitempty"`
	// Translations holds the versions for the other languages written. The embedded
	// Content is the version for the largest of them.
	Translations []StoredContent `json:"translations,omitempty"`
	// VariantB is the second version of a campaign that compares two, with its own
	// translations.
	VariantB *StoredContent `json:"variant_b,omitempty"`
}

// forVariant finds one version of a test. The stored root is version A.
func (c *StoredContent) forVariant(variant string) *StoredContent {
	if c != nil && variant == content.VariantB {
		return c.VariantB
	}
	return c
}

// variants lists the versions that exist, A first.
func (c *StoredContent) variants() []*StoredContent {
	if c.VariantB != nil {
		return []*StoredContent{c, c.VariantB}
	}
	return []*StoredContent{c}
}

// version finds what was written for a language, or nothing.
func (c *StoredContent) version(language string) *StoredContent {
	if c == nil {
		return nil
	}
	if c.Language == language {
		return c
	}
	for i := range c.Translations {
		if c.Translations[i].Language == language {
			return &c.Translations[i]
		}
	}
	return nil
}

// without keeps every version except those of the given languages. The largest left
// becomes the main one. It returns nothing if none is left.
func (c *StoredContent) without(excluded []string) *StoredContent {
	if c == nil {
		return nil
	}
	var root *StoredContent
	for _, version := range c.all() {
		if slices.Contains(excluded, version.Language) {
			continue
		}
		kept := *version
		kept.Translations, kept.VariantB = nil, nil
		if root == nil {
			root = &kept
			continue
		}
		root.Translations = append(root.Translations, kept)
	}
	if root != nil {
		root.VariantB = c.VariantB.without(excluded)
	}
	return root
}

// withVersion puts a changed version back in its place.
func (c StoredContent) withVersion(changed StoredContent) StoredContent {
	if changed.Variant == content.VariantB && c.Variant != content.VariantB && c.VariantB != nil {
		second := c.VariantB.withVersion(changed)
		c.VariantB = &second
		return c
	}
	if c.Language == changed.Language {
		changed.Translations, changed.VariantB = c.Translations, c.VariantB
		return changed
	}
	c.Translations = slices.Clone(c.Translations)
	for i := range c.Translations {
		if c.Translations[i].Language == changed.Language {
			c.Translations[i] = changed
		}
	}
	return c
}

// all lists every version, the main one first.
func (c *StoredContent) all() []*StoredContent {
	versions := []*StoredContent{c}
	for i := range c.Translations {
		versions = append(versions, &c.Translations[i])
	}
	return versions
}

// GenerateInput steers a generation. Instruction is optional. Channel rewrites
// only that channel; with no messages yet, every channel is written anyway.
type GenerateInput struct {
	Instruction string
	Channel     string
	// MissingLanguages writes only the languages with no version yet.
	MissingLanguages bool
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
	channel     string
	cities      []string
	reported    []string
	languages   []string
	// language and reference describe the version one pass is writing.
	language  string
	reference []byte
	// onlyMissing writes the languages with no version and leaves the others as they are.
	onlyMissing bool
	// variant, otherVariant and other describe the second version of a test.
	variant      string
	otherVariant []byte
	other        *StoredContent
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
	channel := input.Channel
	if channel != "" && !slices.Contains(view.Channels, channel) {
		return nil, ErrInvalidChannels
	}
	if view.Content == nil {
		channel = ""
	}
	// The other channel keeps its text, so a new discount here would split the offer.
	if channel != "" && len(view.Channels) > 1 && content.NewDiscount(instruction, view.writingBrief()) {
		return nil, ErrChannelDiscount
	}
	cities, err := s.cities(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.db.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1 AND `+freeToDraft, id, statusGenerating)
	if err != nil {
		return nil, fmt.Errorf("claim generation: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil, ErrGenerationRunning
	}
	languages := view.writingLanguages()
	// A draft from before languages were kept was written for the audience's largest one.
	if view.Content != nil && view.Content.Language == "" {
		view.Content.Language = languages[0]
	}
	// With a test still missing its second version, everything has to be written anyway.
	onlyMissing := input.MissingLanguages && view.Content != nil && (!view.ABTest || view.Content.VariantB != nil)
	if onlyMissing {
		channel, instruction = "", ""
	}
	return &Generation{service: s, view: view, instruction: instruction, channel: channel, cities: cities, languages: languages, onlyMissing: onlyMissing}, nil
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

	stored, attempts, usage, err := g.writeVariants(ctx, emit)
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
	if err := g.save(saveCtx, stored, attempts, usage); err != nil {
		return View{}, err
	}
	return s.Get(saveCtx, g.view.ID)
}

// writeVariants writes the campaign's messages, twice when it compares two versions.
// Like the languages, the second version is kept only if everything passes.
func (g *Generation) writeVariants(ctx context.Context, emit func(GenerationEvent)) (StoredContent, int, ai.Usage, error) {
	if g.onlyMissing {
		return g.writeMissing(ctx, emit)
	}
	emit(GenerationEvent{Type: EventPlan, Text: strings.Join(g.languages, ",")})
	main, attempts, usage, err := g.writeLanguages(ctx, emit, nil)
	if err != nil || !g.view.ABTest {
		return main, attempts, usage, err
	}
	emit(GenerationEvent{Type: EventVariant, Text: content.VariantB})
	second, tried, used, err := g.writeLanguages(ctx, emit, &main)
	attempts += tried
	usage.InputTokens += used.InputTokens
	usage.OutputTokens += used.OutputTokens
	if err != nil {
		return StoredContent{}, attempts, usage, err
	}
	main.VariantB = &second
	return main, attempts, usage, nil
}

// writeMissing adds versions for the languages that have none, saying what the main
// version already says. Everything written before stays as it is.
func (g *Generation) writeMissing(ctx context.Context, emit func(GenerationEvent)) (StoredContent, int, ai.Usage, error) {
	stored := *g.view.Content
	var (
		attempts int
		usage    ai.Usage
	)
	add := func(root *StoredContent, first *StoredContent) error {
		missing := slices.DeleteFunc(slices.Clone(g.languages), func(code string) bool { return root.version(code) != nil })
		if first != nil && len(missing) > 0 {
			emit(GenerationEvent{Type: EventVariant, Text: content.VariantB})
		}
		emit(GenerationEvent{Type: EventPlan, Text: strings.Join(missing, ",")})
		written, tried, used, err := g.writeTranslations(ctx, emit, *root, missing, first)
		attempts += tried
		usage.InputTokens += used.InputTokens
		usage.OutputTokens += used.OutputTokens
		if err == nil {
			root.Translations = append(slices.Clone(root.Translations), written...)
		}
		return err
	}
	if err := add(&stored, nil); err != nil {
		return StoredContent{}, attempts, usage, err
	}
	if g.view.Content.VariantB != nil {
		second := *g.view.Content.VariantB
		if err := add(&second, &stored); err != nil {
			return StoredContent{}, attempts, usage, err
		}
		stored.VariantB = &second
	}
	return stored, attempts, usage, nil
}

// writeLanguages writes one version per language, each checked on its own. The first
// is written and streamed, and the rest then say what it says. Nothing is kept unless
// all of them pass, so an audience is never left with some people's messages out of
// date. With first set it writes the second version of a test, which has to differ.
func (g *Generation) writeLanguages(ctx context.Context, emit func(GenerationEvent), first *StoredContent) (StoredContent, int, ai.Usage, error) {
	language := g.languages[0]
	pass := g.forLanguage(language, first)
	passEmit := emit
	if first != nil {
		passEmit = func(GenerationEvent) {}
		other, err := json.Marshal(pass.limits().Keep(first.Content))
		if err != nil {
			return StoredContent{}, 0, ai.Usage{}, fmt.Errorf("encode other variant: %w", err)
		}
		pass.otherVariant = other
	}
	g.stillRunning(ctx)
	generated, attempts, usage, err := pass.generate(ctx, passEmit)
	if err != nil {
		return StoredContent{}, attempts, usage, err
	}
	main := pass.merged(generated)
	main.Language, main.Variant = language, pass.variant
	emit(GenerationEvent{Type: EventLanguageDone, Text: language})
	// The design is one choice for the whole campaign.
	if first != nil {
		main.EmailTemplate, main.EmailDesign = first.EmailTemplate, first.EmailDesign
	}
	written, tried, used, err := g.writeTranslations(ctx, emit, main, g.languages[1:], first)
	attempts += tried
	usage.InputTokens += used.InputTokens
	usage.OutputTokens += used.OutputTokens
	if err != nil {
		return StoredContent{}, attempts, usage, err
	}
	main.Translations = written
	return main, attempts, usage, nil
}

// writeTranslations writes these languages at once, in the given order, each saying
// what main says. They depend only on main, so waiting for each other would add a
// model call's time per language. The first failure stops the rest.
func (g *Generation) writeTranslations(ctx context.Context, emit func(GenerationEvent), main StoredContent, languages []string, first *StoredContent) ([]StoredContent, int, ai.Usage, error) {
	if len(languages) == 0 {
		return nil, 0, ai.Usage{}, nil
	}
	emit(GenerationEvent{Type: EventLanguage, Text: strings.Join(languages, ",")})
	g.stillRunning(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		attempts int
		usage    ai.Usage
		failure  error
		written  = make([]StoredContent, len(languages))
	)
	for index, language := range languages {
		wg.Go(func() {
			version, tried, used, err := g.writeTranslation(ctx, main, language, first)
			mu.Lock()
			defer mu.Unlock()
			attempts += tried
			usage.InputTokens += used.InputTokens
			usage.OutputTokens += used.OutputTokens
			if err != nil {
				if failure == nil {
					failure = err
				}
				cancel()
				return
			}
			written[index] = version
			emit(GenerationEvent{Type: EventLanguageDone, Text: language})
		})
	}
	wg.Wait()
	if failure != nil {
		return nil, attempts, usage, failure
	}
	return written, attempts, usage, nil
}

func (g *Generation) writeTranslation(ctx context.Context, main StoredContent, language string, first *StoredContent) (StoredContent, int, ai.Usage, error) {
	pass := g.forLanguage(language, first)
	reference, err := json.Marshal(pass.limits().Keep(main.Content))
	if err != nil {
		return StoredContent{}, 0, ai.Usage{}, fmt.Errorf("encode reference: %w", err)
	}
	pass.reference = reference
	generated, attempts, usage, err := pass.generate(ctx, func(GenerationEvent) {})
	if err != nil {
		return StoredContent{}, attempts, usage, err
	}
	version := pass.merged(generated)
	version.Language, version.Variant = language, pass.variant
	// The design is one choice for the whole campaign.
	version.EmailTemplate, version.EmailDesign = main.EmailTemplate, main.EmailDesign
	return version, attempts, usage, nil
}

// stillRunning keeps a long generation from looking abandoned. If it fails the worst
// case is the takeover this exists to make unlikely, so it does not stop the writing.
func (g *Generation) stillRunning(ctx context.Context) {
	if _, err := g.service.db.Exec(ctx, `UPDATE campaigns SET updated_at = now() WHERE id = $1 AND status = $2`, g.view.ID, statusGenerating); err != nil {
		slog.WarnContext(ctx, "mark generation alive", "error", err)
	}
}

// forLanguage is this generation seen from one version's language and limits.
func (g *Generation) forLanguage(language string, first *StoredContent) *Generation {
	pass := *g
	pass.language = language
	if first != nil {
		pass.variant = content.VariantB
		pass.other = first.version(language)
	}
	pass.view.Content = g.view.Content.forVariant(pass.variant).version(language)
	pass.view.ContentLimits = g.view.limitsFor(language)
	// A version with nothing written yet gets every channel.
	if pass.view.Content == nil {
		pass.channel = ""
	}
	return &pass
}

func (g *Generation) generate(ctx context.Context, emit func(GenerationEvent)) (content.Content, int, ai.Usage, error) {
	s := g.service
	request, err := g.request()
	if err != nil {
		return content.Content{}, 0, ai.Usage{}, err
	}
	offerSource := content.OfferSource(g.view.writingBrief(), g.instruction)

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

// limits leaves out the channels this generation does not write.
func (g *Generation) limits() content.Limits {
	limits := g.view.ContentLimits
	switch g.channel {
	case "sms":
		limits.SkipEmail = true
	case "email":
		limits.SkipSMS = true
	}
	return limits
}

func (g *Generation) request() (ai.ContentRequest, error) {
	channels := g.view.Channels
	if g.channel != "" {
		channels = []string{g.channel}
	}
	request := ai.ContentRequest{
		Language:     segment.LanguageName(g.language),
		Reference:    g.reference,
		OtherVariant: g.otherVariant,
		Brief:        g.view.writingBrief(),
		Tone:         g.view.Tone,
		Schema:       content.JSONSchema(channels...),
		Limits:       g.limits(),
		Instruction:  g.instruction,
	}
	for _, condition := range g.view.Conditions {
		request.Conditions = append(request.Conditions, condition.Label+" "+condition.Value)
	}
	if g.view.Content != nil && len(g.reference) == 0 {
		current, err := json.Marshal(g.limits().Keep(g.view.Content.Content))
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
	generated, err := content.ParseGenerated(output, g.limits(), offerSource)
	if err == nil {
		if issues := content.CheckLinks(generated, g.linkSource()); len(issues) > 0 {
			err = &content.ValidationError{Issues: issues}
		} else if issues := content.CheckFacts(generated, g.factSource(), g.cities); len(issues) > 0 {
			err = &content.ValidationError{Issues: issues}
		} else if issues := content.CheckTargeting(generated, g.view.Proposal.Segment.Windows(), g.view.writingBrief()+"\n"+g.instruction); len(issues) > 0 {
			err = &content.ValidationError{Issues: issues}
		} else if issues := g.sameAsOther(generated); len(issues) > 0 {
			err = &content.ValidationError{Issues: issues}
		}
	}
	// Only an instruction can go undone. Without one, a note answers nothing anyone asked.
	if err == nil && g.instruction != "" {
		g.reported = content.ReportedNotApplied(output)
	}
	var invalid *content.ValidationError
	if errors.As(err, &invalid) {
		span.SetStatus(codes.Error, "rejected")
		span.SetAttributes(attribute.StringSlice("validation.reasons", issueReasons(invalid)))
	}
	return generated, err
}

// factSource is everything a message may state a city or number from: what the
// person wrote and the audience it goes to.
func (g *Generation) factSource() string {
	source := g.view.writingBrief() + "\n" + g.instruction
	for _, condition := range g.view.Conditions {
		source += "\n" + condition.Value
	}
	return source
}

// linkSource is everything a person wrote that a generated link may come from,
// including a link they already set on the current version.
func (g *Generation) linkSource() string {
	source := g.view.Brief + "\n" + g.instruction
	if g.view.Content != nil {
		source += "\n" + g.view.Content.EmailCTAURL
	}
	return source + "\n" + string(g.reference) + "\n" + string(g.otherVariant)
}

// sameAsOther refuses a second version that says what the first says, since comparing
// a message with itself tells nobody anything.
func (g *Generation) sameAsOther(generated content.Content) []content.Issue {
	if g.other == nil {
		return nil
	}
	limits := g.limits()
	const reason = "same_as_other_variant"
	var issues []content.Issue
	if !limits.SkipSMS && generated.SMS == g.other.SMS {
		issues = append(issues, content.Issue{Field: content.FieldSMS, Reason: reason, Message: "SMS is the same as the other version; write it differently."})
	}
	if !limits.SkipEmail && generated.EmailSubject == g.other.EmailSubject && generated.EmailBody == g.other.EmailBody {
		issues = append(issues, content.Issue{Field: content.FieldEmailBody, Reason: reason, Message: "Email is the same as the other version; write it differently."})
	}
	return issues
}

func issueReasons(invalid *content.ValidationError) []string {
	reasons := make([]string, 0, len(invalid.Issues))
	for _, issue := range invalid.Issues {
		reasons = append(reasons, issue.Reason)
	}
	return reasons
}

// merged puts newly written messages next to the channel this generation left
// alone, which keeps its text and its record of what a person changed.
func (g *Generation) merged(generated content.Content) StoredContent {
	stored := g.view.Content
	generated.EmailTemplate, generated.EmailDesign = g.view.EmailTemplate, g.view.EmailDesign
	if stored != nil {
		generated.EmailTemplate, generated.EmailDesign = stored.EmailTemplate, stored.EmailDesign
	}
	if g.channel == "" {
		return StoredContent{Content: generated, Edited: []string{}, NotApplied: g.notApplied(generated)}
	}
	merged := StoredContent{Content: generated, Edited: []string{}, NotApplied: append(g.notApplied(generated), notesExcept(stored.NotApplied, g.channel)...)}
	if g.channel == "sms" {
		merged.Content = stored.Content
		merged.SMS = generated.SMS
	} else {
		merged.SMS = stored.SMS
	}
	for _, field := range stored.Edited {
		if (field == content.FieldSMS) == (g.channel == "email") {
			merged.Edited = append(merged.Edited, field)
		}
	}
	return merged
}

// notApplied says what the instruction asked for and these messages do not do:
// what the model admits, and what comparing the text with the instruction shows.
func (g *Generation) notApplied(generated content.Content) []content.Issue {
	channels := g.view.Channels
	if g.channel != "" {
		channels = []string{g.channel}
	}
	notes := []content.Issue{}
	for _, channel := range channels {
		text, field := generated.SMS, content.FieldSMS
		if channel == "email" {
			text = strings.Join([]string{generated.EmailSubject, generated.EmailPreheader, generated.EmailHeadline, generated.EmailBody, generated.EmailCTALabel, generated.EmailCTAURL}, "\n")
			field = content.FieldEmailBody
		}
		parts := slices.Clone(g.reported)
		// A number the model already explained needs no second note.
		for _, missing := range content.MissingFromInstruction(text+"\n"+strings.Join(g.reported, "\n"), g.instruction, g.cities) {
			parts = append(parts, fmt.Sprintf("Your instruction mentions %s, but this message does not.", missing))
		}
		if len(parts) > 0 {
			notes = append(notes, content.Issue{Field: field, Reason: reasonNotApplied, Message: strings.Join(parts, " ")})
		}
	}
	return notes
}

// notesExcept keeps the notes of every channel but this one.
func notesExcept(notes []content.Issue, channel string) []content.Issue {
	kept := []content.Issue{}
	for _, note := range notes {
		if (note.Field == content.FieldSMS) != (channel == "sms") {
			kept = append(kept, note)
		}
	}
	return kept
}

func (g *Generation) save(ctx context.Context, stored StoredContent, attempts int, usage ai.Usage) error {
	s := g.service
	encoded, err := json.Marshal(stored)
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
		"regenerated": g.view.Content != nil, "channel": g.channel, "instruction_given": g.instruction != "", "languages": g.languages,
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
	return s.SaveContentAt(ctx, id, "", edited)
}

// SaveContentAt saves a person's edit made to the messages at revision, the
// ContentRevision they were shown. Every save replaces the whole document, so an edit
// made to an older revision, in another tab or before a regeneration finished, would
// silently undo the newer text; it is refused instead. An empty revision skips that
// check, but a change between this read and the write is still refused.
func (s *Service) SaveContentAt(ctx context.Context, id, revision string, edited content.Content) (View, error) {
	view, err := s.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if view.Status != statusReady || view.Content == nil {
		return View{}, ErrNotEditable
	}
	if revision != "" && revision != view.ContentRevision {
		return View{}, ErrContentChanged
	}
	target := view.Content.forVariant(edited.Variant)
	if target != nil && edited.Language != "" {
		target = target.version(edited.Language)
	}
	if target == nil {
		return View{}, ErrNotEditable
	}
	// Only the name comes from the request. The same design stays as the campaign's copy
	// has it, and a newly chosen one is copied from the library.
	edited.EmailDesign = nil
	switch {
	case edited.EmailTemplate == target.EmailTemplate:
		edited.EmailDesign = target.EmailDesign
	case content.IsCustomTemplate(edited.EmailTemplate):
		edited.EmailDesign, err = s.designForTemplate(ctx, edited.EmailTemplate)
		if err != nil && !errors.Is(err, ErrDesignNotFound) {
			return View{}, err
		}
	}
	limits := view.limitsFor(target.Language)
	edited = limits.Keep(edited)
	edited.Language, edited.Variant = target.Language, target.Variant
	if issues := content.CheckMessages(edited, limits); len(issues) > 0 {
		return View{}, &content.ValidationError{Issues: issues}
	}
	changed := changedFields(target.Content, edited)
	if len(changed) == 0 {
		return view, nil
	}
	// A person who edits a channel has dealt with what the model left undone there.
	notes := target.NotApplied
	for _, field := range changed {
		channel := "email"
		if field == content.FieldSMS {
			channel = "sms"
		}
		notes = notesExcept(notes, channel)
	}
	stored := view.Content.withVersion(StoredContent{Content: edited, Edited: mergeEdited(target.Edited, changed), NotApplied: notes})
	// The design is one choice for the whole campaign.
	for _, root := range stored.variants() {
		for _, version := range root.all() {
			version.EmailTemplate, version.EmailDesign = edited.EmailTemplate, edited.EmailDesign
		}
	}
	encoded, err := json.Marshal(stored)
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
	// The row lock above waits for any other save or generation, so this reads what
	// they left. The edit was made to the document read before, which must still stand.
	var current []byte
	if err := tx.QueryRow(ctx, `SELECT content FROM campaign_versions WHERE campaign_id = $1 AND version = $2`, id, view.Version).Scan(&current); err != nil {
		return View{}, fmt.Errorf("read content: %w", err)
	}
	if contentRevision(current) != view.ContentRevision {
		return View{}, ErrContentChanged
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
		{content.FieldEmailTemplate, before.EmailTemplate, after.EmailTemplate},
	} {
		if field.before != field.after {
			changed = append(changed, field.name)
		}
	}
	return changed
}

func mergeEdited(existing, changed []string) []string {
	merged := []string{}
	for _, field := range []string{content.FieldSMS, content.FieldEmailSubject, content.FieldEmailPreheader, content.FieldEmailHeadline, content.FieldEmailBody, content.FieldEmailCTALabel, content.FieldEmailCTAURL, content.FieldEmailTemplate} {
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

// AudienceBreakdown counts how each condition narrows the audience.
func (s *Service) AudienceBreakdown(ctx context.Context, id string) (segment.Breakdown, error) {
	view, err := s.load(ctx, id)
	if err != nil {
		return segment.Breakdown{}, err
	}
	if view.Proposal == nil {
		return segment.Breakdown{}, ErrNoAudience
	}
	if view.AudienceSnapshot != nil {
		return view.AudienceSnapshot.Breakdown, nil
	}
	return segment.Narrow(ctx, s.db, view.Proposal.Segment, view.AudienceAsOf)
}

// generationAbandoned says whether a GENERATING status outlived its process.
func generationAbandoned(view View) bool {
	return view.Status == statusGenerating && time.Since(view.UpdatedAt) >= abandonedAfter
}
