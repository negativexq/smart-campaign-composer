package campaign_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
)

// The audience part is one the test users match; the code is what is under test.
const (
	audienceBrief = "Send a 30% discount reminder to users in Istanbul who added a product to their cart in the last 30 days and did not buy."
	codeBrief     = audienceBrief + " Give them the code SONBAHAR15."
)

// mockService writes with the built-in test model, the way the demo does.
func mockService(h *harness) *campaign.Service {
	mock := ai.NewMock()
	mock.StreamDelay = 0
	return campaign.NewService(h.pool, mock, h.metrics, noop.NewTracerProvider().Tracer("test"), func() time.Time { return referenceTime })
}

func writtenCampaign(t *testing.T, service *campaign.Service, input campaign.NewCampaign) (string, campaign.View) {
	t.Helper()
	ctx := context.Background()
	id, err := service.CreateWith(ctx, input)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	generation, err := service.StartGeneration(ctx, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("start generation: %v", err)
	}
	view, err := generation.Run(ctx, func(campaign.GenerationEvent) {})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return id, view
}

// Issue #27: a code in the brief reached no message, and the review still passed.
func TestACodeInTheBriefReachesEveryMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	id, view := writtenCampaign(t, service, campaign.NewCampaign{Brief: codeBrief})
	if view.DiscountCode != "SONBAHAR15" || view.FormDiscountCode != nil {
		t.Fatalf("code = %q, form = %v", view.DiscountCode, view.FormDiscountCode)
	}
	versions := 0
	for _, root := range []*campaign.StoredContent{view.Content, view.Content.VariantB} {
		if root == nil {
			continue
		}
		for _, version := range append([]campaign.StoredContent{*root}, root.Translations...) {
			versions++
			if version.DiscountCode != "SONBAHAR15" || !content.HasCode(version.SMS) {
				t.Errorf("%s version: code %q, SMS %q", version.Language, version.DiscountCode, version.SMS)
			}
			email, err := content.RenderEmail(version.Content, "Ayşe", "")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(email.HTML, "SONBAHAR15") || !strings.Contains(email.Text, "SONBAHAR15") {
				t.Errorf("%s version: the email does not show the code", version.Language)
			}
		}
	}
	if versions < 2 {
		t.Fatalf("expected the audience's languages to be written, got %d versions", versions)
	}
	for _, warning := range view.ContentWarnings {
		if warning.Reason == "missing_code" || warning.Reason == "code_changed" {
			t.Errorf("warning on fresh messages: %v", warning)
		}
	}
	approved, err := approveReviewed(context.Background(), service, id)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Content.DiscountCode != "SONBAHAR15" {
		t.Fatalf("approval did not freeze the code: %q", approved.Content.DiscountCode)
	}
}

func TestAFormCodeMustAgreeWithTheBrief(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	code := func(value string) *string { return &value }
	for _, test := range []struct {
		name  string
		input campaign.NewCampaign
		want  error
	}{
		{"another code", campaign.NewCampaign{Brief: codeBrief, DiscountCode: code("KIS20")}, campaign.ErrCodeConflict},
		{"two codes in the brief", campaign.NewCampaign{Brief: "Kodu YAZ10 ya da kodu KIS20 ile gönder."}, campaign.ErrCodeConflict},
		{"not a code", campaign.NewCampaign{Brief: "Yeni koleksiyonu duyur.", DiscountCode: code("SALE 10")}, campaign.ErrInvalidCode},
		{"too short", campaign.NewCampaign{Brief: "Yeni koleksiyonu duyur.", DiscountCode: code("AB")}, campaign.ErrInvalidCode},
	} {
		if _, err := h.service.CreateWith(ctx, test.input); !errors.Is(err, test.want) {
			t.Errorf("%s: %v, want %v", test.name, err, test.want)
		}
	}

	id, err := h.service.CreateWith(ctx, campaign.NewCampaign{Brief: codeBrief, DiscountCode: code(" sonbahar15 ")})
	if err != nil {
		t.Fatalf("the same code in other letters should agree: %v", err)
	}
	view, err := h.service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.FormDiscountCode == nil || *view.FormDiscountCode != "SONBAHAR15" || view.DiscountCode != "SONBAHAR15" {
		t.Fatalf("form %v, code %q", view.FormDiscountCode, view.DiscountCode)
	}

	blank, err := h.service.CreateWith(ctx, campaign.NewCampaign{Brief: "Yeni koleksiyonu duyur.", DiscountCode: code("  ")})
	if err != nil {
		t.Fatalf("a blank code is no code: %v", err)
	}
	if view, err := h.service.Get(ctx, blank); err != nil || view.DiscountCode != "" || view.FormDiscountCode != nil {
		t.Fatalf("blank code stored: %v %q %v", err, view.DiscountCode, view.FormDiscountCode)
	}
}

func TestAFormCodeReachesMessagesWhoseBriefNamesNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	code := "KARGO-BEDAVA"
	_, view := writtenCampaign(t, service, campaign.NewCampaign{Brief: audienceBrief, DiscountCode: &code})
	if view.Content.DiscountCode != code || !content.HasCode(view.Content.SMS) {
		t.Fatalf("code %q, SMS %q", view.Content.DiscountCode, view.Content.SMS)
	}
}

func TestACampaignWithoutACodeWritesAsBefore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	_, view := writtenCampaign(t, service, campaign.NewCampaign{Brief: audienceBrief})
	if view.DiscountCode != "" || view.Content.DiscountCode != "" || content.HasCode(view.Content.SMS) {
		t.Fatalf("a code appeared: %q %q %q", view.DiscountCode, view.Content.DiscountCode, view.Content.SMS)
	}
	email, err := content.RenderEmail(view.Content.Content, "Ayşe", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(email.Text, "discount code") || strings.Contains(email.Text, "kodun") {
		t.Fatalf("an email without a code shows a code box: %q", email.Text)
	}
}

func TestAnEditCannotDropTheCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	ctx := context.Background()
	id, view := writtenCampaign(t, service, campaign.NewCampaign{Brief: codeBrief})

	edited := view.Content.Content
	edited.SMS = "Hi {{first_name}}, your 30% discount is ready."
	edited.DiscountCode = "FREE100"
	var invalid *content.ValidationError
	if _, err := service.SaveContentAt(ctx, id, view.ContentRevision, edited); !errors.As(err, &invalid) || !strings.Contains(err.Error(), "SONBAHAR15") {
		t.Fatalf("an SMS without the code was saved: %v", err)
	}

	// Typing the code out is the same as the variable, and a request cannot change the code.
	edited.SMS = "Hi {{first_name}}, 30% off with SONBAHAR15."
	saved, err := service.SaveContentAt(ctx, id, view.ContentRevision, edited)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Content.SMS != "Hi {{first_name}}, 30% off with {{discount_code}}." || saved.Content.DiscountCode != "SONBAHAR15" {
		t.Fatalf("saved %q with code %q", saved.Content.SMS, saved.Content.DiscountCode)
	}
}

func TestMessagesWrittenForAnEarlierCodeCannotBeApproved(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	ctx := context.Background()
	id, _ := writtenCampaign(t, service, campaign.NewCampaign{Brief: codeBrief})
	// The brief now names another code. The audience stays, so the messages are not
	// written again and still carry the old one.
	if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET brief = replace(brief, 'SONBAHAR15', 'KIS20') WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(view, "code_changed") {
		t.Fatalf("the review page does not say the code changed: %v", view.ContentWarnings)
	}
	var invalid *content.ValidationError
	if _, err := approveReviewed(ctx, service, id); !errors.As(err, &invalid) {
		t.Fatalf("messages for SONBAHAR15 were approved for KIS20: %v", err)
	}
}

func hasWarning(view campaign.View, reason string) bool {
	for _, warning := range view.ContentWarnings {
		if warning.Reason == reason {
			return true
		}
	}
	return false
}

// A brief that says "indirim kodu 15" has no code, and a model reading it wrote
// {{discount_code}} anyway. The writer is told in so many words.
func TestTheWriterIsToldWhenThereIsNoCode(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if brief := provider.contentCalls[0].Brief; !strings.Contains(brief, "No discount code is set") {
		t.Fatalf("brief: %q", brief)
	}
}

// "30 percent" is the same offer as "30%": the messages carry it and pass.
func TestABriefMayWriteItsPercentAsAWord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	_, view := writtenCampaign(t, service, campaign.NewCampaign{Brief: strings.Replace(audienceBrief, "30%", "30 percent", 1)})
	if !slices.Contains(content.Discounts(view.Content.SMS), 30) {
		t.Fatalf("SMS without the offer: %q", view.Content.SMS)
	}
	for _, warning := range view.ContentWarnings {
		if strings.HasSuffix(warning.Reason, "_discount") {
			t.Errorf("warning: %v", warning)
		}
	}
}
