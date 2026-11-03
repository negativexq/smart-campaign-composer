package campaign_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

const namedProposal = `{"campaign_name":"Cart Recovery — İstanbul","segment":{"city":"İstanbul"},"assumptions":[],"unsupported_conditions":[]}`

func TestANewCampaignGetsAProvisionalNameFromItsBrief(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()

	short, err := h.service.Create(ctx, "Winter\n  sale")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	view, err := h.service.Get(ctx, short)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Name != "Winter sale" || view.NameSource != "brief" {
		t.Fatalf("view: %q %q", view.Name, view.NameSource)
	}

	long, err := h.service.Create(ctx, strings.Repeat("reminder ", 40))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	view, err = h.service.Get(ctx, long)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if utf8.RuneCountInString(view.Name) > segment.MaxCampaignNameLength || !strings.HasSuffix(view.Name, "…") {
		t.Fatalf("name: %q", view.Name)
	}
	if strings.Contains(view.Name, "reminde…") {
		t.Fatalf("cut inside a word: %q", view.Name)
	}
}

func TestTheSuggestedNameReplacesTheProvisionalOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: namedProposal})
	id := h.create(t)

	view, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if view.Name != "Cart Recovery — İstanbul" || view.NameSource != "ai" {
		t.Fatalf("view: %q %q", view.Name, view.NameSource)
	}
}

func TestAProposalWithoutANameKeepsTheCurrentOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: namedProposal}, step{output: validProposal})
	ctx := context.Background()
	id := h.create(t)
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	view, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{Refinement: "narrower"})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if view.Name != "Cart Recovery — İstanbul" {
		t.Fatalf("name: %q", view.Name)
	}
}

func TestEditingTheBriefUpdatesAProvisionalName(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	id := h.create(t)
	edited := "Ankara users"
	view, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{Brief: &edited})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if view.Name != edited {
		t.Fatalf("name: %q", view.Name)
	}
}

func TestARenameSurvivesLaterSuggestionsAndIsAudited(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: namedProposal})
	ctx := context.Background()
	id := h.create(t)

	view, err := h.service.Rename(ctx, id, "  Black  Friday ")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if view.Name != "Black Friday" || view.NameSource != "user" {
		t.Fatalf("view: %q %q", view.Name, view.NameSource)
	}

	view, err = h.service.ProposeSegment(ctx, id, campaign.ProposeInput{})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if view.Name != "Black Friday" || view.NameSource != "user" {
		t.Fatalf("suggestion overwrote the name: %q %q", view.Name, view.NameSource)
	}
	if got := h.auditEvents(t, id); strings.Join(got, ",") != "campaign.created,campaign.renamed,segment.proposed" {
		t.Fatalf("audit: %v", got)
	}
}

func TestRenameWorksAfterDraftingBecauseNamesAreNotApprovedContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	id := h.createWithStatus(t, "brief", "COMPLETED")
	view, err := h.service.Rename(context.Background(), id, "Finished")
	if err != nil || view.Name != "Finished" {
		t.Fatalf("rename: %q %v", view.Name, err)
	}
}

func TestRenameRejectsBadInput(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()
	id := h.create(t)

	for _, name := range []string{"  ", strings.Repeat("x", segment.MaxCampaignNameLength+1)} {
		if _, err := h.service.Rename(ctx, id, name); !errors.Is(err, campaign.ErrInvalidName) {
			t.Errorf("%d chars: %v", len(name), err)
		}
	}
	if _, err := h.service.Rename(ctx, "not-a-uuid", "x"); !errors.Is(err, campaign.ErrNotFound) {
		t.Errorf("bad id: %v", err)
	}
	if _, err := h.service.Rename(ctx, "00000000-0000-0000-0000-000000000000", "x"); !errors.Is(err, campaign.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if got := h.auditEvents(t, id); len(got) != 1 {
		t.Fatalf("failed renames must not be audited: %v", got)
	}
}

func TestListSearchesNamesAndBriefs(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()
	id := h.createWithStatus(t, "remind cart users", "DRAFT")
	if _, err := h.service.Rename(ctx, id, "Black Friday"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	for _, query := range []string{"black", "remind"} {
		got, err := h.service.List(ctx, campaign.ListFilter{Query: query})
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		if len(got) != 1 || got[0].Name != "Black Friday" {
			t.Errorf("%q: %+v", query, got)
		}
	}
}
