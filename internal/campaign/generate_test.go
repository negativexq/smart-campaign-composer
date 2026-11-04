package campaign_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
)

const (
	validContent   = `{"sms":"Hi {{first_name}}","email_subject":"Hello {{first_name}}","email_body":"Your cart is waiting."}`
	invalidContent = `{"sms":"Use {{coupon}}","email_subject":"Hello","email_body":"Body"}`
)

// contentProvider scripts StreamContent on top of the segment script.
type contentProvider struct {
	scriptedProvider
	contentMu    sync.Mutex
	contentSteps []step
	contentCalls []ai.ContentRequest
	beforeOutput func(handler ai.StreamHandler)
	// The other languages say what the main version says unless a test wants more.
	lastOutput       string
	translationCalls []ai.ContentRequest
	translationSteps []step
	beforeTranslate  func(request ai.ContentRequest)
}

// translate answers a pass that has another language's version to follow.
func (p *contentProvider) translate(ctx context.Context, request ai.ContentRequest) (ai.Response, error) {
	if p.beforeTranslate != nil {
		p.beforeTranslate(request)
	}
	p.contentMu.Lock()
	defer p.contentMu.Unlock()
	p.translationCalls = append(p.translationCalls, request)
	output := p.lastOutput
	if len(p.translationSteps) > 0 {
		current := p.translationSteps[0]
		p.translationSteps = p.translationSteps[1:]
		if current.err != nil {
			return ai.Response{}, current.err
		}
		output = current.output
	}
	if err := ctx.Err(); err != nil {
		return ai.Response{}, err
	}
	return ai.Response{Output: []byte(output), Usage: ai.Usage{InputTokens: 10, OutputTokens: 5}}, nil
}

func (p *contentProvider) StreamContent(ctx context.Context, request ai.ContentRequest, handler ai.StreamHandler) (ai.Response, error) {
	if len(request.Reference) > 0 || len(request.OtherVariant) > 0 {
		return p.translate(ctx, request)
	}
	p.contentMu.Lock()
	p.contentCalls = append(p.contentCalls, request)
	current := p.contentSteps[0]
	if len(p.contentSteps) > 1 {
		p.contentSteps = p.contentSteps[1:]
	}
	p.contentMu.Unlock()

	if p.beforeOutput != nil {
		p.beforeOutput(handler)
	}
	if err := ctx.Err(); err != nil {
		return ai.Response{}, err
	}
	if current.err != nil {
		return ai.Response{}, current.err
	}
	p.contentMu.Lock()
	p.lastOutput = current.output
	p.contentMu.Unlock()
	handler.OnText(current.output)
	return ai.Response{Output: []byte(current.output), Usage: ai.Usage{InputTokens: 10, OutputTokens: 5}}, nil
}

func newContentHarness(t *testing.T, steps ...step) (*harness, *contentProvider, string) {
	t.Helper()
	h := newHarness(t, step{output: validProposal})
	provider := &contentProvider{scriptedProvider: scriptedProvider{steps: []step{{output: validProposal}, {output: withUpdatedBrief(validProposal, secretBrief)}}}, contentSteps: steps}
	h.service = h.serviceWith(provider)
	id := h.create(t)
	if _, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	return h, provider, id
}

type eventLog struct {
	events []campaign.GenerationEvent
}

func (l *eventLog) add(event campaign.GenerationEvent) { l.events = append(l.events, event) }

func (l *eventLog) text(field string) string {
	var out strings.Builder
	for _, event := range l.events {
		if event.Type == campaign.EventReset {
			out.Reset()
		}
		if event.Type == campaign.EventDelta && event.Field == field {
			out.WriteString(event.Text)
		}
	}
	return out.String()
}

func (l *eventLog) count(eventType string) int {
	count := 0
	for _, event := range l.events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func run(t *testing.T, h *harness, id string, input campaign.GenerateInput) (campaign.View, *eventLog, error) {
	t.Helper()
	generation, err := h.service.StartGeneration(context.Background(), id, input)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	log := &eventLog{}
	view, err := generation.Run(context.Background(), log.add)
	return view, log, err
}

func statusOf(t *testing.T, h *harness, id string) string {
	t.Helper()
	view, err := h.service.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return view.Status
}

func TestGenerationStreamsAndStoresValidatedContent(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})

	view, log, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if log.text(content.FieldSMS) != "Hi {{first_name}}" {
		t.Fatalf("stream: %q", log.text(content.FieldSMS))
	}
	if view.Status != "READY_FOR_REVIEW" || view.Content == nil || view.Content.SMS != "Hi {{first_name}}" {
		t.Fatalf("view: %+v", view)
	}
	if statusOf(t, h, id) != "READY_FOR_REVIEW" {
		t.Fatal("status not stored")
	}
	if got := h.auditEvents(t, id); got[len(got)-1] != "content.generated" {
		t.Fatalf("audit: %v", got)
	}

	request := provider.contentCalls[0]
	if english := provider.translationCalls[0]; english.Limits.SMSName != len("Maximiliano Alexander") {
		t.Fatalf("limits: %+v", english.Limits)
	}
	if len(request.Conditions) == 0 || request.Schema == nil {
		t.Fatalf("request: %+v", request)
	}
	if got := testutil.ToFloat64(h.metrics.LLMRequests.WithLabelValues("content_generation", "scripted", "success")); got != 2 {
		t.Fatalf("one call per language, metric: %v", got)
	}
	if got := testutil.CollectAndCount(h.metrics.LLMTTFT); got != 1 {
		t.Fatalf("ttft series: %d", got)
	}
}

func TestGenerationSpans(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	names := map[string]bool{}
	for _, span := range h.spans.GetSpans() {
		names[span.Name] = true
	}
	for _, want := range []string{"llm.content.generate", "content.validate"} {
		if !names[want] {
			t.Errorf("missing span %s in %v", want, names)
		}
	}
}

func TestGenerationRepairsAndTellsTheStreamToStartOver(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: invalidContent}, step{output: validContent})

	view, log, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if log.count(campaign.EventReset) != 1 || log.text(content.FieldSMS) != "Hi {{first_name}}" {
		t.Fatalf("events: %+v", log.events)
	}
	repair := provider.contentCalls[1]
	if repair.PreviousOutput != invalidContent || !strings.Contains(repair.ValidationError, "coupon") {
		t.Fatalf("repair: %+v", repair)
	}
	if view.Content.SMS != "Hi {{first_name}}" {
		t.Fatalf("view: %+v", view.Content)
	}
	if got := testutil.ToFloat64(h.metrics.ValidationFailures.WithLabelValues("content", "unknown_variable")); got != 1 {
		t.Fatalf("metric: %v", got)
	}
}

func TestAProviderFallbackRestartsTheStream(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})
	provider.beforeOutput = func(handler ai.StreamHandler) {
		handler.OnText(`{"sms":"abandoned`)
		handler.OnRestart()
	}
	_, log, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if log.text(content.FieldSMS) != "Hi {{first_name}}" {
		t.Fatalf("text: %q", log.text(content.FieldSMS))
	}
}

func TestInvalidContentIsNeverStored(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: invalidContent})

	_, _, err := run(t, h, id, campaign.GenerateInput{})

	var invalid *content.ValidationError
	if !errors.As(err, &invalid) || !errors.Is(err, campaign.ErrContentRejected) {
		t.Fatalf("got %v", err)
	}
	if len(provider.contentCalls) != campaign.MaxProposalAttempts {
		t.Fatalf("calls: %d", len(provider.contentCalls))
	}
	view, _ := h.service.Get(context.Background(), id)
	if view.Content != nil || view.Status != "GENERATION_FAILED" {
		t.Fatalf("view: %s %+v", view.Status, view.Content)
	}
	if got := h.auditEvents(t, id); got[len(got)-1] != "content.generation_failed" {
		t.Fatalf("audit: %v", got)
	}
}

func TestProviderFailuresDoNotRepair(t *testing.T) {
	t.Parallel()
	for _, failure := range []error{ai.ErrRefused, ai.ErrTruncated, ai.ErrRateLimited, ai.ErrUnavailable} {
		h, provider, id := newContentHarness(t, step{err: failure})
		_, _, err := run(t, h, id, campaign.GenerateInput{})
		if !errors.Is(err, failure) || len(provider.contentCalls) != 1 {
			t.Errorf("%v: got %v after %d calls", failure, err, len(provider.contentCalls))
		}
		if statusOf(t, h, id) != "GENERATION_FAILED" {
			t.Errorf("%v: status %s", failure, statusOf(t, h, id))
		}
	}
}

func TestAFailedGenerationCanBeRetried(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{err: ai.ErrUnavailable}, step{output: validContent})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err == nil {
		t.Fatal("first run should fail")
	}
	view, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil || view.Status != "READY_FOR_REVIEW" {
		t.Fatalf("retry: %v %s", err, view.Status)
	}
}

func TestAFailedRegenerationKeepsTheExistingContent(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent}, step{err: ai.ErrUnavailable})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{Instruction: "shorter"}); err == nil {
		t.Fatal("second run should fail")
	}
	view, _ := h.service.Get(context.Background(), id)
	if view.Content == nil || view.Status != "READY_FOR_REVIEW" {
		t.Fatalf("view: %s %+v", view.Status, view.Content)
	}
}

func TestCancellingPutsTheCampaignBackWithoutFailing(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})
	ctx, cancel := context.WithCancel(context.Background())
	provider.beforeOutput = func(ai.StreamHandler) { cancel() }

	generation, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := generation.Run(ctx, (&eventLog{}).add); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}

	if statusOf(t, h, id) != "DRAFT" {
		t.Fatalf("status: %s", statusOf(t, h, id))
	}
	for _, event := range h.auditEvents(t, id) {
		if event == "content.generation_failed" {
			t.Fatal("a person stopping generation is not a failure")
		}
	}
}

func TestOnlyOneGenerationAtATime(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	ctx := context.Background()
	if _, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{}); !errors.Is(err, campaign.ErrGenerationRunning) {
		t.Fatalf("second start: %v", err)
	}
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); !errors.Is(err, campaign.ErrGenerationRunning) {
		t.Fatalf("propose during generation: %v", err)
	}
}

func TestAnAbandonedGenerationCanBeTakenOver(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	ctx := context.Background()
	if _, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The process died mid-generation and left the status behind.
	if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET updated_at = now() - interval '1 hour' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("takeover: %v", err)
	}
}

func TestGenerationPreconditions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("no audience", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, step{output: validProposal})
		h.service = h.serviceWith(&contentProvider{contentSteps: []step{{output: validContent}}})
		id := h.create(t)
		if _, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{}); !errors.Is(err, campaign.ErrNoAudience) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("blocked audience", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, step{output: validProposal})
		blocked := `{"segment":{"city":"İstanbul"},"assumptions":[],"unsupported_conditions":["age"]}`
		h.service = h.serviceWith(&contentProvider{scriptedProvider: scriptedProvider{steps: []step{{output: blocked}}}, contentSteps: []step{{output: validContent}}})
		id := h.create(t)
		if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
			t.Fatalf("propose: %v", err)
		}
		if _, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{}); !errors.Is(err, campaign.ErrAudienceBlocked) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("past drafting", func(t *testing.T) {
		t.Parallel()
		h, _, id := newContentHarness(t, step{output: validContent})
		if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET status = 'DISPATCHING' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{}); !errors.Is(err, campaign.ErrNotEditable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("unknown campaign", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, step{output: validProposal})
		if _, err := h.service.StartGeneration(ctx, "nope", campaign.GenerateInput{}); !errors.Is(err, campaign.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestRegenerationPassesCurrentContentAndInstruction(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{Instruction: "shorter"}); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	request := provider.contentCalls[1]
	if request.Instruction != "shorter" || !strings.Contains(string(request.Current), "Hi {{first_name}}") {
		t.Fatalf("request: %+v", request)
	}
}

func TestAnInstructionCanChangeTheDiscount(t *testing.T) {
	t.Parallel()
	const withForty = `{"sms":"40% off","email_subject":"40% off","email_body":"40% off"}`
	h, _, id := newContentHarness(t, step{output: withForty})
	view, _, err := run(t, h, id, campaign.GenerateInput{Instruction: "use 40% instead"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(view.ContentWarnings) == 0 || view.ContentWarnings[0].Reason != "invented_discount" {
		t.Fatalf("the difference from the brief must stay visible: %+v", view.ContentWarnings)
	}
}

func TestEditingContent(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	ctx := context.Background()
	generated, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	edited := generated.Content.Content
	edited.SMS = "Edited by {{first_name}}"
	view, err := h.service.SaveContent(ctx, id, edited)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if view.Content.SMS != edited.SMS || strings.Join(view.Content.Edited, ",") != "sms" {
		t.Fatalf("view: %+v", view.Content)
	}
	if got := h.auditEvents(t, id); got[len(got)-1] != "content.edited" {
		t.Fatalf("audit: %v", got)
	}

	before := len(h.auditEvents(t, id))
	if _, err := h.service.SaveContent(ctx, id, edited); err != nil {
		t.Fatalf("repeat save: %v", err)
	}
	if len(h.auditEvents(t, id)) != before {
		t.Fatal("saving nothing new must not add to the audit trail")
	}
}

func TestEditsFollowTheSameHardRulesButMayChangeTheOffer(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	ctx := context.Background()
	generated, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	tooLong := generated.Content.Content
	tooLong.SMS = strings.Repeat("x", 161)
	var invalid *content.ValidationError
	if _, err := h.service.SaveContent(ctx, id, tooLong); !errors.As(err, &invalid) {
		t.Fatalf("got %v", err)
	}

	offer := generated.Content.Content
	offer.SMS = "Now 50% off"
	view, err := h.service.SaveContent(ctx, id, offer)
	if err != nil {
		t.Fatalf("a person may change the offer: %v", err)
	}
	if len(view.ContentWarnings) == 0 {
		t.Fatal("but the mismatch must be pointed out")
	}
}

func TestContentCannotBeEditedOutsideReview(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	if _, err := h.service.SaveContent(context.Background(), id, content.Content{SMS: "a", EmailSubject: "b", EmailBody: "c"}); !errors.Is(err, campaign.ErrNotEditable) {
		t.Fatalf("got %v", err)
	}
}

func TestChangingTheAudienceClearsContent(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	view, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{Refinement: "narrower"})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if view.Content != nil || view.Status != "DRAFT" {
		t.Fatalf("view: %s %+v", view.Status, view.Content)
	}
}

func TestContentLimitsAreExposed(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	view, err := h.service.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.ContentLimits.SMSName != 21 || view.ContentLimits.EmailName != 21 {
		t.Fatalf("limits: %+v", view.ContentLimits)
	}
}

func TestSampleRecipientsShowTheEdgeCases(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	samples, err := h.service.SampleRecipients(context.Background(), id)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	got := map[string]bool{}
	for _, sample := range samples {
		got[sample.FirstName] = true
		if sample.ID == seed.FixtureNoConsent {
			t.Fatal("a user without consent will never see this message")
		}
	}
	if !got["Maximiliano Alexander"] || !got[""] {
		t.Fatalf("samples: %+v", samples)
	}
	languages := map[string]bool{}
	for _, sample := range samples {
		languages[sample.Language] = true
	}
	if !languages["tr"] || !languages["en"] {
		t.Fatalf("each language shows its own people: %+v", samples)
	}
}

func TestAModelCannotMakeUpWhereAButtonLeads(t *testing.T) {
	t.Parallel()
	invented := `{"sms":"Hi {{first_name}}","email_subject":"Hello {{first_name}}","email_body":"Your cart is waiting.","email_cta_label":"Go","email_cta_url":"https://evil.example.com"}`
	h, provider, id := newContentHarness(t, step{output: invented}, step{output: validContent})

	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("the corrected attempt should pass: %v", err)
	}
	if len(provider.contentCalls) != 2 || !strings.Contains(provider.contentCalls[1].ValidationError, "not in the brief") {
		t.Fatalf("calls: %+v", provider.contentCalls)
	}
	view, err := h.service.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Content == nil || view.Content.EmailCTAURL != "" {
		t.Fatalf("content: %+v", view.Content)
	}
}

func TestAModelMayUseALinkFromTheInstruction(t *testing.T) {
	t.Parallel()
	given := `{"sms":"Hi {{first_name}}","email_subject":"Hello {{first_name}}","email_body":"Your cart is waiting.","email_cta_label":"Go","email_cta_url":"https://shop.example.com/sale"}`
	h, _, id := newContentHarness(t, step{output: given})

	if _, _, err := run(t, h, id, campaign.GenerateInput{Instruction: "Link to https://shop.example.com/sale"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	view, err := h.service.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Content == nil || view.Content.EmailCTAURL != "https://shop.example.com/sale" {
		t.Fatalf("content: %+v", view.Content)
	}
}

func TestRegeneratingOneChannelLeavesTheOtherAlone(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent}, step{output: `{"sms":"New {{first_name}}"}`})
	ctx := context.Background()
	first, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	edited := first.Content.Content
	edited.SMS = "My {{first_name}}"
	edited.EmailSubject = "My subject"
	if _, err := h.service.SaveContent(ctx, id, edited); err != nil {
		t.Fatalf("save: %v", err)
	}

	view, _, err := run(t, h, id, campaign.GenerateInput{Channel: "sms"})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if view.Content.SMS != "New {{first_name}}" {
		t.Fatalf("sms: %q", view.Content.SMS)
	}
	if view.Content.EmailSubject != "My subject" || view.Content.EmailBody != first.Content.EmailBody {
		t.Fatalf("the email changed: %+v", view.Content)
	}
	if !slices.Equal(view.Content.Edited, []string{"email_subject"}) {
		t.Fatalf("edited: %v", view.Content.Edited)
	}
	request := provider.contentCalls[len(provider.contentCalls)-1]
	if !request.Limits.SkipEmail || request.Limits.SkipSMS {
		t.Fatalf("limits: %+v", request.Limits)
	}
	if strings.Contains(string(request.Current), "My subject") {
		t.Fatalf("the model was shown the other channel: %s", request.Current)
	}
}

func TestRegeneratingTheEmailKeepsTheSMS(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent}, step{output: `{"email_subject":"New subject","email_body":"New body"}`})
	first, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	view, _, err := run(t, h, id, campaign.GenerateInput{Channel: "email"})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if view.Content.SMS != first.Content.SMS || view.Content.EmailSubject != "New subject" || view.Content.EmailBody != "New body" {
		t.Fatalf("content: %+v", view.Content)
	}
}

func TestAFirstGenerationWritesEveryChannel(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})

	if _, _, err := run(t, h, id, campaign.GenerateInput{Channel: "email"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if request := provider.contentCalls[0]; request.Limits.SkipSMS || request.Limits.SkipEmail {
		t.Fatalf("limits: %+v", request.Limits)
	}
}

func TestOnlyAChosenChannelCanBeRegenerated(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	for _, channel := range []string{"push", "SMS"} {
		if _, err := h.service.StartGeneration(context.Background(), id, campaign.GenerateInput{Channel: channel}); !errors.Is(err, campaign.ErrInvalidChannels) {
			t.Fatalf("%s: got %v", channel, err)
		}
	}
}

func TestTheChosenEmailTemplateIsSavedAndSurvivesRegenerating(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	first, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	chosen := first.Content.Content
	chosen.EmailTemplate = "banner"
	saved, err := h.service.SaveContent(context.Background(), id, chosen)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Content.EmailTemplate != "banner" || !slices.Contains(saved.Content.Edited, "email_template") {
		t.Fatalf("saved: %+v", saved.Content)
	}

	for _, channel := range []string{"sms", "email", ""} {
		view, _, err := run(t, h, id, campaign.GenerateInput{Channel: channel})
		if err != nil {
			t.Fatalf("regenerate %q: %v", channel, err)
		}
		if view.Content.EmailTemplate != "banner" {
			t.Fatalf("regenerate %q lost the template: %+v", channel, view.Content)
		}
	}
}

func TestOneChannelCannotBeGivenADifferentDiscount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	provider := &contentProvider{scriptedProvider: scriptedProvider{steps: []step{{output: validProposal}}}, contentSteps: []step{
		{output: `{"sms":"30% {{first_name}}","email_subject":"30% off","email_body":"Your 30% is waiting."}`},
	}}
	h.service = h.serviceWith(provider)
	ctx := context.Background()
	id, err := h.service.Create(ctx, secretBrief+" with a 30% discount")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{Channel: "sms", Instruction: "make it 50%"}); !errors.Is(err, campaign.ErrChannelDiscount) {
		t.Fatalf("got %v", err)
	}
	view, err := h.service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "READY_FOR_REVIEW" {
		t.Fatalf("status: %s", view.Status)
	}
}

func TestAHandEditedDiscountMustMatchAcrossChannelsToApprove(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	provider := &contentProvider{scriptedProvider: scriptedProvider{steps: []step{{output: validProposal}}}, contentSteps: []step{
		{output: `{"sms":"30% {{first_name}}","email_subject":"30% off","email_body":"Your 30% is waiting."}`},
	}}
	h.service = h.serviceWith(provider)
	ctx := context.Background()
	id, err := h.service.Create(ctx, secretBrief+" with a 30% discount")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatal(err)
	}
	first, _, err := run(t, h, id, campaign.GenerateInput{})
	if err == nil {
		edited := first.Content.Content
		edited.SMS = "50% {{first_name}}"
		if _, err = h.service.SaveContent(ctx, id, edited); err != nil {
			t.Fatalf("save: %v", err)
		}
		if _, err = h.service.Approve(ctx, id); !errors.Is(err, campaign.ErrChannelDiscount) {
			t.Fatalf("approve: %v", err)
		}
		return
	}
	t.Fatalf("run: %v", err)
}

func TestGeneratedMessagesAreCheckedAgainstTheBrief(t *testing.T) {
	t.Parallel()
	for name, output := range map[string]string{
		"another city": `{"sms":"Ankara'ya özel {{first_name}}","email_subject":"Hello","email_body":"Text"}`,
		"a deadline":   `{"sms":"Son 3 gün {{first_name}}","email_subject":"Hello","email_body":"Text"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, provider, id := newContentHarness(t, step{output: output})
			if _, _, err := run(t, h, id, campaign.GenerateInput{}); !errors.Is(err, campaign.ErrContentRejected) {
				t.Fatalf("got %v", err)
			}
			if len(provider.contentCalls) != campaign.MaxProposalAttempts {
				t.Fatalf("calls: %d", len(provider.contentCalls))
			}
		})
	}
}

func TestAMessageSayingHowThePersonWasPickedIsRepaired(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t,
		step{output: `{"sms":"Hi {{first_name}}, you carted in the last 30 days","email_subject":"Hello","email_body":"Your cart is waiting."}`},
		step{output: validContent})
	view, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if view.Content.SMS != "Hi {{first_name}}" || !strings.Contains(provider.contentCalls[1].ValidationError, "picked") {
		t.Fatalf("sms %q, repair %q", view.Content.SMS, provider.contentCalls[1].ValidationError)
	}
}

func TestAnInstructionTheModelSkippedIsShown(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t,
		step{output: validContent},
		step{output: `{"sms":"Hello {{first_name}}","not_applied":["I could not mention the store"]}`},
		step{output: `{"sms":"Hi again {{first_name}}"}`})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	view, _, err := run(t, h, id, campaign.GenerateInput{Channel: "sms", Instruction: "say it lasts 3 days and mention the store"})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	notes := []string{}
	for _, warning := range view.ContentWarnings {
		if warning.Reason == "not_applied" {
			notes = append(notes, warning.Message)
		}
	}
	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "the store") || !strings.Contains(joined, "3") {
		t.Fatalf("warnings: %+v", view.ContentWarnings)
	}

	view, _, err = run(t, h, id, campaign.GenerateInput{Channel: "sms"})
	if err != nil {
		t.Fatalf("again: %v", err)
	}
	if slices.ContainsFunc(view.ContentWarnings, func(w content.Issue) bool { return w.Reason == "not_applied" }) {
		t.Fatalf("a later generation keeps the warning: %+v", view.ContentWarnings)
	}
}

func TestEachLanguageInTheAudienceGetsItsOwnVersion(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})
	provider.translationSteps = []step{{output: `{"sms":"Hey {{first_name}}","email_subject":"Hey {{first_name}}","email_body":"Still here."}`}}
	view, log, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	total := 0
	for _, language := range view.Languages {
		total += language.Total
	}
	if total != view.Audience.Total {
		t.Fatalf("languages %+v do not add up to %+v", view.Languages, view.Audience)
	}
	if view.Content.Language != "tr" || len(view.Content.Translations) != 1 || view.Content.Translations[0].Language != "en" {
		t.Fatalf("versions: %+v", view.Content)
	}
	if view.Content.Translations[0].SMS != "Hey {{first_name}}" || view.Content.SMS != "Hi {{first_name}}" {
		t.Fatalf("messages: %+v", view.Content)
	}
	if provider.contentCalls[0].Language != "Turkish" || len(provider.contentCalls[0].Reference) != 0 {
		t.Fatalf("main pass: %+v", provider.contentCalls[0])
	}
	english := provider.translationCalls[0]
	if english.Language != "English" || !strings.Contains(string(english.Reference), "Hi {{first_name}}") {
		t.Fatalf("second pass: %+v", english)
	}
	saw := false
	for _, event := range log.events {
		saw = saw || (event.Type == campaign.EventLanguage && event.Text == "en")
	}
	if !saw {
		t.Fatalf("events: %+v", log.events)
	}
}

func TestALanguageThatFailsKeepsNothing(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})
	failure := errors.New("provider down")
	for range 5 {
		provider.translationSteps = append(provider.translationSteps, step{err: failure})
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err == nil {
		t.Fatal("a missing language must fail the whole generation")
	}
	view, err := h.service.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Content != nil {
		t.Fatalf("half of the messages were kept: %+v", view.Content)
	}
}

func TestEditingOneLanguageLeavesTheOthers(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	generated, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	edited := generated.Content.Translations[0].Content
	edited.SMS = "Only in English {{first_name}}"
	view, err := h.service.SaveContent(context.Background(), id, edited)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if view.Content.SMS != generated.Content.SMS || len(view.Content.Edited) != 0 {
		t.Fatalf("main version changed: %+v", view.Content)
	}
	if english := view.Content.Translations[0]; english.SMS != edited.SMS || strings.Join(english.Edited, ",") != "sms" {
		t.Fatalf("english: %+v", english)
	}
}

// Version A's English, then version B in Turkish and English.
func secondVersionSteps() []step {
	return []step{
		{output: `{"sms":"Hey {{first_name}}","email_subject":"Hey {{first_name}}","email_body":"Still here."}`},
		{output: `{"sms":"Merhaba B {{first_name}}","email_subject":"Selam {{first_name}}","email_body":"Sepetin duruyor."}`},
		{output: `{"sms":"Hello B {{first_name}}","email_subject":"Hello B {{first_name}}","email_body":"Your basket is here."}`},
	}
}

func newTestHarness(t *testing.T, steps ...step) (*harness, *contentProvider, string) {
	t.Helper()
	h, provider, _ := newContentHarness(t, steps...)
	ctx := context.Background()
	id, err := h.service.CreateWith(ctx, campaign.NewCampaign{Brief: secretBrief, ABTest: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	provider.steps = []step{{output: validProposal}, {output: withUpdatedBrief(validProposal, secretBrief)}}
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	return h, provider, id
}

func TestATestWritesASecondVersionForEveryLanguage(t *testing.T) {
	t.Parallel()
	h, provider, id := newTestHarness(t, step{output: validContent})
	provider.translationSteps = secondVersionSteps()
	view, log, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !view.ABTest {
		t.Fatal("the option was not kept")
	}
	second := view.Content.VariantB
	if second == nil || second.SMS != "Merhaba B {{first_name}}" || second.Variant != "B" || second.Language != "tr" {
		t.Fatalf("second version: %+v", second)
	}
	if len(second.Translations) != 1 || second.Translations[0].SMS != "Hello B {{first_name}}" || second.Translations[0].Variant != "B" {
		t.Fatalf("second version's languages: %+v", second.Translations)
	}
	first := provider.translationCalls[1]
	if !strings.Contains(string(first.OtherVariant), "Hi {{first_name}}") {
		t.Fatalf("the writer did not see the other version: %s", first.OtherVariant)
	}
	if log.count(campaign.EventVariant) != 1 {
		t.Fatalf("events: %+v", log.events)
	}
	if strings.Contains(log.text("sms"), "Merhaba B") {
		t.Fatalf("the second version must not stream over the first: %s", log.text("sms"))
	}
	reloaded, err := h.service.Get(context.Background(), id)
	if err != nil || reloaded.Content.VariantB == nil || !reloaded.ABTest {
		t.Fatalf("not stored: %+v %v", reloaded.Content, err)
	}
}

func TestASecondVersionSayingTheSameThingIsWrittenAgain(t *testing.T) {
	t.Parallel()
	h, provider, id := newTestHarness(t, step{output: validContent})
	steps := secondVersionSteps()
	provider.translationSteps = []step{steps[0], {output: validContent}, steps[1], steps[2]}
	view, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if view.Content.VariantB.SMS == view.Content.SMS {
		t.Fatalf("both versions say: %s", view.Content.SMS)
	}
	if len(provider.translationCalls) != 4 {
		t.Fatalf("calls: %d", len(provider.translationCalls))
	}
}

func TestAFailedSecondVersionKeepsNothing(t *testing.T) {
	t.Parallel()
	h, provider, id := newTestHarness(t, step{output: validContent})
	provider.translationSteps = []step{secondVersionSteps()[0]}
	for range 5 {
		provider.translationSteps = append(provider.translationSteps, step{err: errors.New("provider down")})
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err == nil {
		t.Fatal("a missing second version must fail the whole generation")
	}
	view, err := h.service.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Content != nil {
		t.Fatalf("half of the test was kept: %+v", view.Content)
	}
}

func TestEditingOneVersionLeavesTheOther(t *testing.T) {
	t.Parallel()
	h, provider, id := newTestHarness(t, step{output: validContent})
	provider.translationSteps = secondVersionSteps()
	generated, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	edited := generated.Content.VariantB.Translations[0].Content
	edited.SMS = "Only in B {{first_name}}"
	view, err := h.service.SaveContent(context.Background(), id, edited)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if view.Content.VariantB.Translations[0].SMS != edited.SMS {
		t.Fatalf("not saved: %+v", view.Content.VariantB)
	}
	if view.Content.SMS != generated.Content.SMS || view.Content.Translations[0].SMS != generated.Content.Translations[0].SMS || view.Content.VariantB.SMS != generated.Content.VariantB.SMS {
		t.Fatalf("another version changed: %+v", view.Content)
	}
	first := generated.Content.Content
	first.SMS = "Only in A {{first_name}}"
	view, err = h.service.SaveContent(context.Background(), id, first)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if view.Content.SMS != first.SMS || view.Content.VariantB == nil || view.Content.VariantB.Translations[0].SMS != edited.SMS {
		t.Fatalf("saving A lost B: %+v", view.Content)
	}
}

func TestATestCannotBeApprovedWithoutOrWithADifferentOffer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, provider, id := newTestHarness(t, step{output: validContent})
	provider.translationSteps = secondVersionSteps()
	generated, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	edited := generated.Content.VariantB.Content
	edited.SMS = "%20 indirim {{first_name}}"
	if _, err := h.service.SaveContent(ctx, id, edited); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := h.service.Approve(ctx, id); !errors.Is(err, campaign.ErrLanguageDiscount) {
		t.Fatalf("different offers: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `UPDATE campaign_versions SET content = content - 'variant_b' WHERE campaign_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Approve(ctx, id); !errors.Is(err, campaign.ErrMissingVariant) {
		t.Fatalf("no second version: %v", err)
	}
}

func TestEachPersonStaysOnOneSideOfATest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, provider, id := newTestHarness(t, step{output: validContent})
	provider.translationSteps = secondVersionSteps()
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	before, err := h.service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(before.VariantAudience) == 0 {
		t.Fatal("no sides counted")
	}
	picked := before.VariantAudience[0].Variant
	for _, side := range before.VariantAudience {
		var people, reachable int
		if err := h.pool.QueryRow(ctx, `
			SELECT count(DISTINCT d.user_id), count(*) FILTER (WHERE d.status <> 'SKIPPED' AND d.channel = 'sms')
			FROM campaign_deliveries d JOIN users u ON u.id = d.user_id
			WHERE d.campaign_id = $1 AND d.variant = $2 AND u.language = $3`, id, side.Variant, side.Language).Scan(&people, &reachable); err != nil {
			t.Fatal(err)
		}
		if people != side.Total || reachable != side.SMS {
			t.Fatalf("approval counted %+v, sending used %d people and %d SMS", side, people, reachable)
		}
	}
	sides := map[string]bool{}
	for _, side := range before.VariantAudience {
		sides[side.Variant] = true
	}
	var mixed, groups, empty int
	if err := h.pool.QueryRow(ctx, `
		SELECT count(DISTINCT variant), count(*) FILTER (WHERE variant IS NULL)
		FROM campaign_deliveries WHERE campaign_id = $1`, id).Scan(&groups, &empty); err != nil {
		t.Fatal(err)
	}
	if groups != len(sides) || empty != 0 {
		t.Fatalf("groups %d, without a side %d", groups, empty)
	}
	if err := h.pool.QueryRow(ctx, `
		SELECT count(*) FROM (SELECT user_id, count(DISTINCT variant) AS sides FROM campaign_deliveries WHERE campaign_id = $1 GROUP BY user_id) people
		WHERE sides > 1`, id).Scan(&mixed); err != nil {
		t.Fatal(err)
	}
	if mixed != 0 {
		t.Fatalf("%d people got both versions", mixed)
	}

	only, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Variant: picked, Limit: 1000})
	if err != nil || len(only) == 0 {
		t.Fatalf("filter: %d %v", len(only), err)
	}
	for _, delivery := range only {
		if delivery.Variant != picked {
			t.Fatalf("filter returned: %+v", delivery)
		}
	}
	if _, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Variant: "C"}); !errors.Is(err, campaign.ErrUnknownFilter) {
		t.Fatalf("unknown side: %v", err)
	}

	if _, err := h.pool.Exec(ctx, `UPDATE campaign_deliveries SET status = 'SENT' WHERE id = $1`, only[0].ID); err != nil {
		t.Fatal(err)
	}
	report, err := h.service.Report(ctx, id)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	accepted, planned := 0, 0
	for _, row := range report.Variants {
		planned += row.Planned
		if row.Variant == picked && row.Channel == only[0].Channel {
			accepted += row.Accepted
		}
	}
	if accepted != 1 || planned == 0 {
		t.Fatalf("report: %+v", report.Variants)
	}
}

func TestACampaignWithOneVersionHasNoSides(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, _, id := newContentHarness(t, step{output: validContent})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	report, err := h.service.Report(ctx, id)
	if err != nil || len(report.Variants) != 0 {
		t.Fatalf("report: %+v %v", report.Variants, err)
	}
}

func TestALongGenerationKeepsSayingItIsAlive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, provider, id := newTestHarness(t, step{output: validContent})
	provider.translationSteps = secondVersionSteps()
	provider.beforeOutput = func(ai.StreamHandler) {
		if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET updated_at = now() - interval '10 minutes' WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	}
	var stale bool
	provider.beforeTranslate = func(request ai.ContentRequest) {
		if len(request.OtherVariant) == 0 {
			return
		}
		if err := h.pool.QueryRow(ctx, `SELECT updated_at < now() - interval '5 minutes' FROM campaigns WHERE id = $1`, id).Scan(&stale); err != nil {
			t.Error(err)
		}
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stale {
		t.Fatal("a running generation looked abandoned")
	}
}

func TestTheShareEnteredDecidesTheSplit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, provider, _ := newContentHarness(t, step{output: validContent})
	share := 20
	id, err := h.service.CreateWith(ctx, campaign.NewCampaign{Brief: secretBrief, ABTest: true, VariantAPercent: &share})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	provider.steps = []step{{output: validProposal}, {output: withUpdatedBrief(validProposal, secretBrief)}}
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	provider.translationSteps = secondVersionSteps()
	view, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if view.VariantAPercent != 20 {
		t.Fatalf("share: %d", view.VariantAPercent)
	}
	// The audience here is small and the hash is the campaign's own, so a side can be
	// empty at any one share. A larger share can only ever take in more people.
	onA := func(share int) int {
		t.Helper()
		if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET variant_a_percent = $2 WHERE id = $1`, id, share); err != nil {
			t.Fatal(err)
		}
		current, err := h.service.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		people := 0
		for _, side := range current.VariantAudience {
			if side.Variant == "A" {
				people += side.Total
			}
		}
		return people
	}
	few, chosen, most := onA(1), onA(20), onA(99)
	if few > chosen || chosen > most || few >= most {
		t.Fatalf("people on A at 1%%, 20%% and 99%%: %d, %d, %d", few, chosen, most)
	}
	if onA(20) != chosen {
		t.Fatal("the same share moved people")
	}
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var sent int
	if err := h.pool.QueryRow(ctx, `SELECT count(DISTINCT user_id) FROM campaign_deliveries WHERE campaign_id = $1 AND variant = 'A'`, id).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if sent != chosen {
		t.Fatalf("approval counted %d, sending used %d", chosen, sent)
	}
}

func TestAShareMustLeaveSomeoneOnBothSides(t *testing.T) {
	t.Parallel()
	h, _, _ := newContentHarness(t, step{output: validContent})
	for _, share := range []int{0, 100, -5} {
		if _, err := h.service.CreateWith(context.Background(), campaign.NewCampaign{Brief: secretBrief, ABTest: true, VariantAPercent: &share}); !errors.Is(err, campaign.ErrInvalidOptions) {
			t.Fatalf("share %d: %v", share, err)
		}
	}
	share := 30
	if _, err := h.service.CreateWith(context.Background(), campaign.NewCampaign{Brief: secretBrief, VariantAPercent: &share}); !errors.Is(err, campaign.ErrInvalidOptions) {
		t.Fatalf("a share without a test: %v", err)
	}
}
