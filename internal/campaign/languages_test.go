package campaign_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
)

// readersOf moves fixture people to another language after the audience is proposed.
func readersOf(t *testing.T, h *harness, code string, ids ...int64) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(), `UPDATE users SET language = $1 WHERE id = ANY($2)`, code, ids); err != nil {
		t.Fatal(err)
	}
}

func versionLanguages(view campaign.View) []string {
	if view.Content == nil {
		return nil
	}
	languages := []string{view.Content.Language}
	for _, translation := range view.Content.Translations {
		languages = append(languages, translation.Language)
	}
	return languages
}

func TestAnExcludedLanguageKeepsItsPeopleAndGetsNoVersion(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	readersOf(t, h, "de", seed.FixtureCartNoPurchase)
	readersOf(t, h, "fr", seed.FixtureTurkishName)
	ctx := context.Background()
	before, err := h.service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	after, err := h.service.SetExcludedLanguages(ctx, id, []string{"fr"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Audience != before.Audience {
		t.Fatalf("the audience changed: %+v, %+v", before.Audience, after.Audience)
	}
	view, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatal(err)
	}
	languages := versionLanguages(view)
	if slices.Contains(languages, "fr") || !slices.Contains(languages, "de") || !slices.Contains(languages, "en") {
		t.Fatalf("versions: %v", languages)
	}
}

func TestEnglishIsWrittenWhenOnlyTheLeftOutNeedIt(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	readersOf(t, h, "tr", seed.FixtureLongName)
	readersOf(t, h, "de", seed.FixtureCartNoPurchase)
	if _, err := h.service.SetExcludedLanguages(context.Background(), id, []string{"de"}); err != nil {
		t.Fatal(err)
	}
	view, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatal(err)
	}
	if languages := versionLanguages(view); !slices.Equal(languages, []string{"tr", "en"}) {
		t.Fatalf("versions: %v", languages)
	}
}

func TestOnlyLanguagesOtherThanEnglishCanBeLeftOut(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	for _, excluded := range [][]string{{"en"}, {"xx"}} {
		if _, err := h.service.SetExcludedLanguages(context.Background(), id, excluded); !errors.Is(err, campaign.ErrInvalidLanguages) {
			t.Fatalf("%v: %v", excluded, err)
		}
	}
}

func TestLeavingOutALanguageRemovesItsVersion(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	readersOf(t, h, "de", seed.FixtureCartNoPurchase)
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The main version goes too: the largest left takes its place.
	view, err := h.service.SetExcludedLanguages(ctx, id, []string{"de", "tr"})
	if err != nil {
		t.Fatal(err)
	}
	if languages := versionLanguages(view); !slices.Equal(languages, []string{"en"}) {
		t.Fatalf("versions: %v", languages)
	}
	if view.Status != "READY_FOR_REVIEW" {
		t.Fatalf("status: %s", view.Status)
	}
	if _, err := approveReviewed(ctx, h.service, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := h.service.SetExcludedLanguages(ctx, id, nil); !errors.Is(err, campaign.ErrNotEditable) {
		t.Fatalf("changed after approval: %v", err)
	}
}

func TestOnlyMissingLanguagesAreWrittenWhenOneIsAdded(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})
	readersOf(t, h, "de", seed.FixtureCartNoPurchase)
	ctx := context.Background()
	if _, err := h.service.SetExcludedLanguages(ctx, id, []string{"de"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.SetExcludedLanguages(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := approveReviewed(ctx, h.service, id); !errors.Is(err, campaign.ErrMissingLanguage) {
		t.Fatalf("approved without a version: %v", err)
	}
	mainCalls := len(provider.contentCalls)
	provider.translationSteps = []step{{output: `{"sms":"Hallo {{first_name}}","email_subject":"Hallo {{first_name}}","email_body":"Ihr Warenkorb wartet."}`}}
	view, _, err := run(t, h, id, campaign.GenerateInput{MissingLanguages: true, Instruction: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.contentCalls) != mainCalls {
		t.Fatalf("the main version was written again: %d calls", len(provider.contentCalls))
	}
	if !slices.Contains(versionLanguages(view), "de") || view.Content.SMS != "Hi {{first_name}}" {
		t.Fatalf("versions: %v, main: %q", versionLanguages(view), view.Content.SMS)
	}
	if _, err := approveReviewed(ctx, h.service, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

func TestManyLanguagesAreWrittenAtOnceAndKeepTheirOrder(t *testing.T) {
	t.Parallel()
	h, provider, id := newContentHarness(t, step{output: validContent})
	readersOf(t, h, "de", seed.FixtureCartNoPurchase)
	readersOf(t, h, "fr", seed.FixtureTurkishName)
	// Every translation waits until all of them have started.
	arrived := make(chan struct{}, 3)
	release := make(chan struct{})
	provider.beforeTranslate = func(ai.ContentRequest) {
		arrived <- struct{}{}
		<-release
	}
	go func() {
		for range 2 {
			<-arrived
		}
		close(release)
	}()
	view, log, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatal(err)
	}
	finished := []string{}
	plan := ""
	for _, event := range log.events {
		switch event.Type {
		case campaign.EventPlan:
			plan = event.Text
		case campaign.EventLanguageDone:
			finished = append(finished, event.Text)
		}
	}
	slices.Sort(finished)
	wanted := strings.Split(plan, ",")
	slices.Sort(wanted)
	if len(wanted) != 4 || !slices.Equal(finished, wanted) {
		t.Fatalf("plan %q, finished %v", plan, finished)
	}
	want := []string{}
	for _, audience := range view.Languages {
		if audience.SMS+audience.Email > 0 {
			want = append(want, audience.Language)
		}
	}
	if languages := versionLanguages(view); !slices.Equal(languages, want) {
		t.Fatalf("versions: %v, want %v", languages, want)
	}
}

func TestEnglishLimitsCountTheNamesOfThoseWhoFallBackToIt(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	// The longest name in the audience now reads French, which is left out.
	readersOf(t, h, "fr", seed.FixtureLongName)
	readersOf(t, h, "en", seed.FixtureCartNoPurchase)
	ctx := context.Background()
	if _, err := h.service.SetExcludedLanguages(ctx, id, []string{"fr"}); err != nil {
		t.Fatal(err)
	}
	view, _, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatal(err)
	}
	var english *campaign.StoredContent
	for i := range view.Content.Translations {
		if view.Content.Translations[i].Language == "en" {
			english = &view.Content.Translations[i]
		}
	}
	if english == nil {
		english = view.Content
	}
	edited := english.Content
	edited.SMS = "{{first_name}} " + strings.Repeat("a", content.MaxSMS-10)
	var invalid *content.ValidationError
	if _, err := h.service.SaveContent(ctx, id, edited); !errors.As(err, &invalid) {
		t.Fatalf("a long name that falls back to English was not counted: %v", err)
	}
}

func TestACopyKeepsItsLanguageChoice(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	readersOf(t, h, "de", seed.FixtureCartNoPurchase)
	ctx := context.Background()
	if _, err := h.service.SetExcludedLanguages(ctx, id, []string{"de"}); err != nil {
		t.Fatal(err)
	}
	copied, err := h.service.Duplicate(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(copied.ExcludedLanguages, []string{"de"}) {
		t.Fatalf("excluded: %v", copied.ExcludedLanguages)
	}
}

// Before copying, a marketer sees how many of the copy's audience this campaign
// already reached, and a rule for people messaged lately leaves them out.
func TestADuplicatePreviewCountsWhoTheCampaignAlreadyReached(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `
		UPDATE campaign_deliveries SET status = 'SENT', sent_at = $2, updated_at = $2
		WHERE campaign_id = $1 AND status = 'QUEUED'`, id, referenceTime.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	plain, err := h.service.DuplicatePreview(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.ReachedBefore == 0 || plain.LastSentAt == nil || plain.NotMessagedWithinDays != nil {
		t.Fatalf("a copy as it is would reach the same people again: %+v", plain)
	}
	days := 10
	ruled, err := h.service.DuplicatePreview(ctx, id, &days)
	if err != nil {
		t.Fatal(err)
	}
	if ruled.ReachedBefore != 0 || ruled.Audience.Total != plain.Audience.Total-plain.ReachedBefore {
		t.Fatalf("the rule should leave out exactly the %d reached: %+v", plain.ReachedBefore, ruled)
	}
	copied, err := h.service.Duplicate(ctx, id, &days)
	if err != nil {
		t.Fatal(err)
	}
	if copied.Audience.Total != ruled.Audience.Total {
		t.Fatalf("the copy's audience is not the one previewed: %+v", copied.Audience)
	}
}

func TestLeavingOutTheOnlyLanguageWrittenLeavesADraft(t *testing.T) {
	t.Parallel()
	h, _, id := newContentHarness(t, step{output: validContent})
	ctx := context.Background()
	// Nobody falls back to English, so only Turkish is written.
	if _, err := h.pool.Exec(ctx, `UPDATE users SET language = 'tr'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.SetExcludedLanguages(ctx, id, []string{"tr"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "DRAFT" || view.Content != nil {
		t.Fatalf("status %s, content %v", view.Status, versionLanguages(view))
	}
}
