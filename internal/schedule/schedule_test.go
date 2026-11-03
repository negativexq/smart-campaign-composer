package schedule_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/schedule"
)

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) // a Tuesday

func at(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return &parsed
}

func TestOnceRunsOnce(t *testing.T) {
	t.Parallel()
	spec := schedule.Spec{Kind: schedule.KindOnce, At: at("2026-11-05T09:00:00+03:00")}
	next, ok := spec.Next(now)
	if !ok || !next.Equal(*spec.At) {
		t.Fatalf("got %v %v", next, ok)
	}
	if _, ok := spec.Next(*spec.At); ok {
		t.Fatal("must not run again")
	}
}

func TestIntervalCountsFromItsStart(t *testing.T) {
	t.Parallel()
	spec := schedule.Spec{Kind: schedule.KindInterval, EveryHours: 6, StartsAt: at("2026-11-03T10:00:00Z")}
	for _, tt := range []struct{ after, want string }{
		{"2026-11-03T09:00:00Z", "2026-11-03T10:00:00Z"},
		{"2026-11-03T10:00:00Z", "2026-11-03T16:00:00Z"},
		{"2026-11-03T15:59:00Z", "2026-11-03T16:00:00Z"},
		// After an outage it continues on the same grid instead of catching up.
		{"2026-11-05T01:00:00Z", "2026-11-05T04:00:00Z"},
	} {
		next, ok := spec.Next(*at(tt.after))
		if !ok || !next.Equal(*at(tt.want)) {
			t.Errorf("after %s: got %v, want %s", tt.after, next, tt.want)
		}
	}
}

func TestWeeklyUsesItsOwnTimeZone(t *testing.T) {
	t.Parallel()
	spec := schedule.Spec{Kind: schedule.KindWeekly, Days: []string{"mon", "wed"}, Time: "09:30", Timezone: "Europe/Istanbul"}
	for _, tt := range []struct{ after, want string }{
		{"2026-11-03T12:00:00Z", "2026-11-04T06:30:00Z"}, // Tuesday noon: Wednesday
		{"2026-11-04T06:30:00Z", "2026-11-09T06:30:00Z"}, // that exact moment: next Monday
		{"2026-11-04T06:29:00Z", "2026-11-04T06:30:00Z"},
	} {
		next, ok := spec.Next(*at(tt.after))
		if !ok || !next.Equal(*at(tt.want)) {
			t.Errorf("after %s: got %v, want %s", tt.after, next, tt.want)
		}
	}
}

func TestWeeklyKeepsLocalTimeAcrossClockChanges(t *testing.T) {
	t.Parallel()
	spec := schedule.Spec{Kind: schedule.KindWeekly, Days: []string{"sun"}, Time: "09:00", Timezone: "America/New_York"}
	first, _ := spec.Next(*at("2026-10-30T00:00:00Z")) // Nov 1 is the change to winter time
	second, _ := spec.Next(first)
	if !first.Equal(*at("2026-11-01T14:00:00Z")) || !second.Equal(*at("2026-11-08T14:00:00Z")) {
		t.Fatalf("got %v and %v", first, second)
	}
	before, _ := spec.Next(*at("2026-10-24T00:00:00Z"))
	if !before.Equal(*at("2026-10-25T13:00:00Z")) {
		t.Fatalf("summer time: got %v", before)
	}
}

func TestATimeTheClockSkipsStillMovesForward(t *testing.T) {
	t.Parallel()
	spec := schedule.Spec{Kind: schedule.KindWeekly, Days: []string{"sun"}, Time: "02:30", Timezone: "America/New_York"}
	after := *at("2027-03-13T12:00:00Z") // 02:30 does not exist on Sunday March 14
	next, ok := spec.Next(after)
	if !ok || !next.After(after) || next.After(after.Add(48*time.Hour)) {
		t.Fatalf("got %v %v", next, ok)
	}
}

func TestEndsAtStopsARepeatingSchedule(t *testing.T) {
	t.Parallel()
	spec := schedule.Spec{Kind: schedule.KindInterval, EveryHours: 24, StartsAt: at("2026-11-03T10:00:00Z"), EndsAt: at("2026-11-05T10:00:00Z")}
	if next, ok := spec.Next(*at("2026-11-04T10:00:00Z")); !ok || !next.Equal(*spec.EndsAt) {
		t.Fatalf("the end is inclusive: %v %v", next, ok)
	}
	if _, ok := spec.Next(*spec.EndsAt); ok {
		t.Fatal("must stop after the end")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	valid := []schedule.Spec{
		{Kind: schedule.KindOnce, At: at("2026-11-04T00:00:00Z")},
		{Kind: schedule.KindInterval, EveryHours: 1},
		{Kind: schedule.KindInterval, EveryHours: 24, StartsAt: at("2026-11-04T00:00:00Z"), EndsAt: at("2026-12-01T00:00:00Z")},
		{Kind: schedule.KindWeekly, Days: []string{"sat"}, Time: "00:00", Timezone: "UTC"},
	}
	for _, spec := range valid {
		if err := spec.Validate(now); err != nil {
			t.Errorf("%+v: %v", spec, err)
		}
	}
	invalid := map[string]schedule.Spec{
		"unknown kind":      {Kind: "cron"},
		"once without time": {Kind: schedule.KindOnce},
		"once in the past":  {Kind: schedule.KindOnce, At: at("2026-11-03T11:00:00Z")},
		"too far ahead":     {Kind: schedule.KindOnce, At: at("2028-01-01T00:00:00Z")},
		"no interval":       {Kind: schedule.KindInterval},
		"interval too long": {Kind: schedule.KindInterval, EveryHours: 10000},
		"no days":           {Kind: schedule.KindWeekly, Time: "09:00", Timezone: "UTC"},
		"unknown day":       {Kind: schedule.KindWeekly, Days: []string{"funday"}, Time: "09:00", Timezone: "UTC"},
		"bad time":          {Kind: schedule.KindWeekly, Days: []string{"mon"}, Time: "25:00", Timezone: "UTC"},
		"bad zone":          {Kind: schedule.KindWeekly, Days: []string{"mon"}, Time: "09:00", Timezone: "Mars/Olympus"},
		"ends before start": {Kind: schedule.KindInterval, EveryHours: 1, StartsAt: at("2026-11-05T00:00:00Z"), EndsAt: at("2026-11-04T00:00:00Z")},
		"ends in the past":  {Kind: schedule.KindInterval, EveryHours: 1, EndsAt: at("2026-11-01T00:00:00Z")},
	}
	for name, spec := range invalid {
		var problem *schedule.InvalidError
		if err := spec.Validate(now); !errors.As(err, &problem) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestDescribeReadsLikeASentence(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		spec schedule.Spec
		want string
	}{
		{schedule.Spec{Kind: schedule.KindInterval, EveryHours: 1}, "Every hour"},
		{schedule.Spec{Kind: schedule.KindInterval, EveryHours: 6}, "Every 6 hours"},
		{schedule.Spec{Kind: schedule.KindInterval, EveryHours: 48}, "Every 2 days"},
		{schedule.Spec{Kind: schedule.KindWeekly, Days: []string{"wed", "mon"}, Time: "09:30", Timezone: "Europe/Istanbul"}, "Monday and Wednesday at 09:30 (Europe/Istanbul)"},
		{schedule.Spec{Kind: schedule.KindWeekly, Days: []string{"mon", "tue", "wed"}, Time: "08:00", Timezone: "UTC"}, "Monday, Tuesday and Wednesday at 08:00 (UTC)"},
	} {
		if got := tt.spec.Describe(); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
	once := schedule.Spec{Kind: schedule.KindOnce, At: at("2026-11-05T09:00:00+03:00")}
	if got := once.Describe(); !strings.HasPrefix(got, "Once") {
		t.Errorf("got %q", got)
	}
}
