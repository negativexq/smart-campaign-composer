package campaign_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/schedule"
)

func entriesOf(calendar campaign.Calendar, id string) []campaign.CalendarEntry {
	var entries []campaign.CalendarEntry
	for _, entry := range calendar.Entries {
		if entry.CampaignID == id {
			entries = append(entries, entry)
		}
	}
	return entries
}

// A draft sits on the day it last changed, and once it sends, its run takes its place.
func TestTheCalendarShowsADraftThenItsRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, id := readyCampaign(t)
	// Drafts are placed by when the database last changed them, which is now.
	from, to := time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour)

	calendar, err := h.service.Calendar(ctx, campaign.ListFilter{}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	entries := entriesOf(calendar, id)
	if len(entries) != 1 || entries[0].Kind != campaign.EntryDraft || entries[0].Run != nil {
		t.Fatalf("before sending: %+v", entries)
	}
	if len(calendar.Campaigns) != 1 || calendar.Campaigns[0].Preview == nil || calendar.Campaigns[0].Preview.SMS == "" {
		t.Fatalf("campaigns: %+v", calendar.Campaigns)
	}

	if _, err := approveReviewed(ctx, h.service, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	calendar, err = h.service.Calendar(ctx, campaign.ListFilter{}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	entries = entriesOf(calendar, id)
	if len(entries) != 1 || entries[0].Kind != campaign.EntrySending || entries[0].Run == nil || entries[0].Run.Number != 1 {
		t.Fatalf("after sending: %+v", entries)
	}

	// A range that ends before the run started shows nothing of it. The service's
	// clock, and so the run's start, is referenceTime.
	calendar, err = h.service.Calendar(ctx, campaign.ListFilter{}, referenceTime.Add(-48*time.Hour), referenceTime.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(calendar.Entries) != 0 || len(calendar.Campaigns) != 0 {
		t.Fatalf("earlier range: %+v", calendar)
	}
}

// A repeating campaign shows every send its rule plans in the range, not only the next.
func TestTheCalendarShowsEverySendARuleWillMake(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, id := approvedCampaign(t)
	spec := schedule.Spec{Kind: schedule.KindWeekly, Days: []string{"mon", "thu"}, Time: "09:30", Timezone: "Europe/Istanbul"}
	if _, err := h.service.Schedule(ctx, id, spec); err != nil {
		t.Fatal(err)
	}
	// Two weeks from Monday 9 November 2026 in Istanbul: four sends.
	istanbul, _ := time.LoadLocation("Europe/Istanbul")
	from := time.Date(2026, 11, 9, 0, 0, 0, 0, istanbul)
	calendar, err := h.service.Calendar(ctx, campaign.ListFilter{}, from, from.AddDate(0, 0, 14))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entriesOf(calendar, id) {
		if entry.Kind != campaign.EntryScheduled {
			t.Fatalf("kind: %+v", entry)
		}
		got = append(got, entry.At.In(istanbul).Format("Mon 2 Jan 15:04"))
	}
	want := []string{"Mon 9 Nov 09:30", "Thu 12 Nov 09:30", "Mon 16 Nov 09:30", "Thu 19 Nov 09:30"}
	if len(got) != len(want) {
		t.Fatalf("sends: %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sends: %v, want %v", got, want)
		}
	}

	// The list's filters apply: a search nothing matches leaves the calendar empty.
	calendar, err = h.service.Calendar(ctx, campaign.ListFilter{Query: "no campaign is called this"}, from, from.AddDate(0, 0, 14))
	if err != nil {
		t.Fatal(err)
	}
	if len(calendar.Entries) != 0 {
		t.Fatalf("filtered: %+v", calendar.Entries)
	}
}

func TestACalendarRangeIsBounded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	now := time.Now()
	for name, to := range map[string]time.Time{"reversed": now.Add(-time.Hour), "empty": now, "too long": now.AddDate(0, 0, 44)} {
		if _, err := h.service.Calendar(context.Background(), campaign.ListFilter{}, now, to); !errors.Is(err, campaign.ErrCalendarRange) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestOccurrencesStopAtTheLimitAndSayItDid(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	hourly := schedule.Spec{Kind: schedule.KindInterval, EveryHours: 1, StartsAt: &start}
	from := start.Add(10 * time.Hour)

	sends, more := campaign.Occurrences(hourly, start, from, from.Add(5*time.Hour), 10)
	if more || len(sends) != 5 || !sends[0].Equal(from) {
		t.Fatalf("within the limit: %v %v", sends, more)
	}
	sends, more = campaign.Occurrences(hourly, start, from, from.Add(24*time.Hour), 10)
	if !more || len(sends) != 10 {
		t.Fatalf("past the limit: %d %v", len(sends), more)
	}
	ends := start.Add(12 * time.Hour)
	hourly.EndsAt = &ends
	sends, more = campaign.Occurrences(hourly, start, from, from.Add(24*time.Hour), 10)
	if more || len(sends) != 3 {
		t.Fatalf("ending rule: %v %v", sends, more)
	}
}
