package campaign_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/schedule"
)

func listedIDs(t *testing.T, h *harness, filter campaign.ListFilter) []string {
	t.Helper()
	rows, err := h.service.List(context.Background(), filter)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := []string{}
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestTagsKeepOneSpellingAndFilterTheList(t *testing.T) {
	t.Parallel()
	h, first := readyCampaign(t)
	ctx := context.Background()
	second := h.create(t)

	view, err := h.service.SetTags(ctx, first, []string{"Black  Friday", " sale ", "SALE"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(view.Tags, []string{"Black Friday", "sale"}) {
		t.Fatalf("tags %q", view.Tags)
	}
	// Another campaign spelling a known tag differently gets the known spelling.
	view, err = h.service.SetTags(ctx, second, []string{"Sale"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(view.Tags, []string{"sale"}) {
		t.Fatalf("second tags %q", view.Tags)
	}

	for _, tags := range [][]string{{""}, {strings.Repeat("x", 31)}, {"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}} {
		if _, err := h.service.SetTags(ctx, first, tags); !errors.Is(err, campaign.ErrInvalidTags) {
			t.Errorf("%q: %v", tags, err)
		}
	}

	uses, err := h.service.Tags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []campaign.TagUse{{Tag: "Black Friday", Campaigns: 1}, {Tag: "sale", Campaigns: 2}}
	if !slices.Equal(uses, want) {
		t.Fatalf("tag uses %v, want %v", uses, want)
	}
	if got := listedIDs(t, h, campaign.ListFilter{Tag: "Black Friday"}); !slices.Equal(got, []string{first}) {
		t.Fatalf("tag filter listed %v", got)
	}
	if got := listedIDs(t, h, campaign.ListFilter{Query: "black fri"}); !slices.Equal(got, []string{first}) {
		t.Fatalf("search by tag listed %v", got)
	}
	if events := h.auditEvents(t, second); !slices.Contains(events, "campaign.tags_changed") {
		t.Fatalf("audit %v", events)
	}
}

func TestArchivingCancelsAScheduleThatNeverRanAndRestoringKeepsItCancelled(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindInterval, EveryHours: 24}); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.Archive(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.ArchivedAt == nil || view.Status != "APPROVED" || view.Schedule != nil {
		t.Fatalf("archived view: archived %v status %s schedule %v", view.ArchivedAt, view.Status, view.Schedule)
	}
	if _, err := h.service.Archive(ctx, id); !errors.Is(err, campaign.ErrArchived) {
		t.Fatalf("second archive: %v", err)
	}
	// Nothing sends from the archive.
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindInterval, EveryHours: 24}); !errors.Is(err, campaign.ErrArchived) {
		t.Fatalf("schedule while archived: %v", err)
	}
	if got := listedIDs(t, h, campaign.ListFilter{}); slices.Contains(got, id) {
		t.Fatal("archived campaign is in the list")
	}
	if got := listedIDs(t, h, campaign.ListFilter{Group: "archived"}); !slices.Equal(got, []string{id}) {
		t.Fatalf("archive listed %v", got)
	}
	counts, err := h.service.CountGroups(ctx, campaign.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if counts["archived"] != 1 || counts["all"] != 0 {
		t.Fatalf("counts %v", counts)
	}

	view, err = h.service.Unarchive(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.ArchivedAt != nil || view.Status != "APPROVED" || view.Schedule != nil {
		t.Fatalf("restored view: archived %v status %s schedule %v", view.ArchivedAt, view.Status, view.Schedule)
	}
	if _, err := h.service.Unarchive(ctx, id); !errors.Is(err, campaign.ErrNotArchived) {
		t.Fatalf("second restore: %v", err)
	}
	events := h.auditEvents(t, id)
	if !slices.Contains(events, "campaign.archived") || !slices.Contains(events, "campaign.unarchived") {
		t.Fatalf("audit %v", events)
	}
}

func TestACampaignStillSendingCannotBeArchived(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindInterval, EveryHours: 24}); err != nil {
		t.Fatal(err)
	}
	if ran, err := scheduler(h, referenceTime.Add(24*time.Hour+time.Second)).RunDue(ctx); err != nil || ran != 1 {
		t.Fatalf("run: %d %v", ran, err)
	}
	if _, err := h.service.Archive(ctx, id); !errors.Is(err, campaign.ErrStillSending) {
		t.Fatalf("archive while sending: %v", err)
	}
	// It sent, so it cannot be deleted either: its deliveries stay on record.
	if err := h.service.Delete(ctx, id); !errors.Is(err, campaign.ErrSentBefore) {
		t.Fatalf("delete after sending: %v", err)
	}
}

func TestOnlyACampaignThatNeverSentCanBeDeleted(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	ctx := context.Background()
	scheduled := h.create(t)
	if _, err := h.service.SetTags(ctx, id, []string{"gone"}); err != nil {
		t.Fatal(err)
	}

	if err := h.service.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Get(ctx, id); !errors.Is(err, campaign.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if err := h.service.Delete(ctx, id); !errors.Is(err, campaign.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if got := listedIDs(t, h, campaign.ListFilter{}); slices.Contains(got, id) || !slices.Contains(got, scheduled) {
		t.Fatalf("list after delete %v", got)
	}
	if uses, err := h.service.Tags(ctx); err != nil || len(uses) != 0 {
		t.Fatalf("tags of a deleted campaign: %v %v", uses, err)
	}
	// The audit trail cannot change, so it keeps the delete.
	if events := h.auditEvents(t, id); events[len(events)-1] != "campaign.deleted" {
		t.Fatalf("audit %v", events)
	}
}

func TestAScheduledCampaignCannotBeDeleted(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindInterval, EveryHours: 24}); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Delete(ctx, id); !errors.Is(err, campaign.ErrSentBefore) {
		t.Fatalf("delete while scheduled: %v", err)
	}
}
