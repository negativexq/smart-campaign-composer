package campaign_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
)

const (
	shopLink  = "https://shop.example.com/sonbahar"
	linkBrief = audienceBrief + " Link: " + shopLink
)

// Issue #29: the brief's link reached the email's button and no SMS.
func TestABriefsLinkReachesEveryMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	id, view := writtenCampaign(t, service, campaign.NewCampaign{Brief: linkBrief})
	if view.Link != shopLink {
		t.Fatalf("link = %q", view.Link)
	}
	versions := 0
	for _, root := range []*campaign.StoredContent{view.Content, view.Content.VariantB} {
		if root == nil {
			continue
		}
		for _, version := range append([]campaign.StoredContent{*root}, root.Translations...) {
			versions++
			if version.Link != shopLink || !content.HasLink(version.SMS) || strings.Contains(version.SMS, shopLink) {
				t.Errorf("%s version: link %q, SMS %q", version.Language, version.Link, version.SMS)
			}
			if version.EmailCTAURL != shopLink {
				t.Errorf("%s version: button opens %q", version.Language, version.EmailCTAURL)
			}
		}
	}
	if versions < 2 {
		t.Fatalf("expected the audience's languages to be written, got %d versions", versions)
	}
	if hasWarning(view, "missing_link") || hasWarning(view, "link_changed") {
		t.Fatalf("warning on fresh messages: %v", view.ContentWarnings)
	}
	approved, err := approveReviewed(context.Background(), service, id)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Content.Link != shopLink {
		t.Fatalf("approval did not freeze the link: %q", approved.Content.Link)
	}
}

func TestABriefWithTwoLinksIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, err := h.service.CreateWith(context.Background(), campaign.NewCampaign{Brief: linkBrief + " Or https://shop.example.com/kis"})
	if !errors.Is(err, campaign.ErrLinkConflict) {
		t.Fatalf("two links: %v", err)
	}
	if _, err := h.service.CreateWith(context.Background(), campaign.NewCampaign{Brief: linkBrief + " Again: " + shopLink + "."}); err != nil {
		t.Fatalf("the same link twice is one link: %v", err)
	}
}

func TestAnEditCannotDropTheLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	ctx := context.Background()
	id, view := writtenCampaign(t, service, campaign.NewCampaign{Brief: linkBrief})

	edited := view.Content.Content
	edited.SMS = "Hi {{first_name}}, your 30% discount is ready."
	var invalid *content.ValidationError
	if _, err := service.SaveContentAt(ctx, id, view.ContentRevision, edited); !errors.As(err, &invalid) || !strings.Contains(err.Error(), shopLink) {
		t.Fatalf("an SMS without the link was saved: %v", err)
	}
	edited.SMS = view.Content.SMS
	edited.EmailCTAURL = "https://shop.example.com/other"
	if _, err := service.SaveContentAt(ctx, id, view.ContentRevision, edited); !errors.As(err, &invalid) {
		t.Fatalf("an email whose button opens another link was saved: %v", err)
	}

	// Typing the link out is the same as the variable, and a request cannot change the link.
	edited.EmailCTAURL = shopLink
	edited.SMS = "Hi {{first_name}}, 30% off: " + shopLink
	edited.Link = "https://evil.example.com"
	saved, err := service.SaveContentAt(ctx, id, view.ContentRevision, edited)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Content.SMS != "Hi {{first_name}}, 30% off: {{link}}" || saved.Content.Link != shopLink {
		t.Fatalf("saved %q with link %q", saved.Content.SMS, saved.Content.Link)
	}
}

func TestMessagesWrittenForAnEarlierLinkCannotBeApproved(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	service := mockService(h)
	ctx := context.Background()
	id, _ := writtenCampaign(t, service, campaign.NewCampaign{Brief: linkBrief})
	if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET brief = replace(brief, '/sonbahar', '/kis') WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(view, "link_changed") {
		t.Fatalf("the review page does not say the link changed: %v", view.ContentWarnings)
	}
	var invalid *content.ValidationError
	if _, err := approveReviewed(ctx, service, id); !errors.As(err, &invalid) {
		t.Fatalf("messages for the old link were approved: %v", err)
	}
}
