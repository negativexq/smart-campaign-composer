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
}

func (p *contentProvider) StreamContent(ctx context.Context, request ai.ContentRequest, handler ai.StreamHandler) (ai.Response, error) {
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
	if request.Limits.SMSName != len("Maximiliano Alexander") {
		t.Fatalf("limits: %+v", request.Limits)
	}
	if len(request.Conditions) == 0 || request.Schema == nil {
		t.Fatalf("request: %+v", request)
	}
	if got := testutil.ToFloat64(h.metrics.LLMRequests.WithLabelValues("content_generation", "scripted", "success")); got != 1 {
		t.Fatalf("metric: %v", got)
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
		blocked := `{"segment":{"city":"Ankara"},"assumptions":[],"unsupported_conditions":["age"]}`
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
	if !got["Maximiliano Alexander"] || !got[""] || len(samples) != 3 {
		t.Fatalf("samples: %+v", samples)
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
