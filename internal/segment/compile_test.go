package segment_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

var referenceTime = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func ptr[T any](value T) *T { return &value }

func seededDatabase(t *testing.T, users int) (*pgxpool.Pool, seed.Dataset) {
	t.Helper()
	pool := testsupport.NewDatabase(t)
	dataset := seed.Generate(referenceTime, users)
	if _, err := seed.Apply(context.Background(), pool, dataset); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return pool, dataset
}

func exampleSpec() segment.Spec {
	return segment.Spec{
		City:                ptr(seed.CityIstanbul),
		CartAddedWithinDays: ptr(30),
		NoPurchaseSinceCart: ptr(true),
	}
}

func TestCompilePassesEveryValueAsParameter(t *testing.T) {
	t.Parallel()
	hostile := "x'; DROP TABLE users; --"
	spec := segment.Spec{City: &hostile, CartAddedWithinDays: ptr(30)}

	where, args := segment.Compile(spec, referenceTime)

	if strings.Contains(where, "DROP") || strings.Contains(where, hostile) {
		t.Fatalf("value leaked into sql: %s", where)
	}
	if len(args) != 2 || args[0] != hostile {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestCountMatchesExampleBriefFixtures(t *testing.T) {
	t.Parallel()
	// Only fixtures, so the numbers are exact: 9 users match, of whom one has no
	// consent, one is SMS-only and one is email-only.
	pool, _ := seededDatabase(t, 13)

	counts, err := segment.Count(context.Background(), pool, exampleSpec(), referenceTime)
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	want := segment.Counts{Total: 9, SMS: 7, Email: 7}
	if counts != want {
		t.Fatalf("got %+v, want %+v", counts, want)
	}
}

func TestMatchingUserIDsExcludeTheRightFixtures(t *testing.T) {
	t.Parallel()
	pool, _ := seededDatabase(t, 13)

	ids, err := segment.MatchingUserIDs(context.Background(), pool, exampleSpec(), referenceTime)
	if err != nil {
		t.Fatalf("match: %v", err)
	}

	got := map[int64]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for id, want := range map[int64]bool{
		seed.FixtureCartNoPurchase:         true,
		seed.FixturePurchasedAfterCart:     false,
		seed.FixturePurchaseBeforeCart:     true,
		seed.FixtureCartTooOld:             false,
		seed.FixtureOtherCity:              false,
		seed.FixtureCartAgainAfterPurchase: true,
		seed.FixtureOldCartThenPurchase:    false,
		seed.FixtureNoConsent:              true,
	} {
		if got[id] != want {
			t.Errorf("user %d: matched=%v, want %v", id, got[id], want)
		}
	}
}

func TestCountHandlesEachFilterAlone(t *testing.T) {
	t.Parallel()
	pool, dataset := seededDatabase(t, 300)
	ctx := context.Background()

	cartWithin := func(id int64, days int) bool {
		for _, event := range dataset.CartEvents {
			if event.UserID == id && !event.OccurredAt.Before(referenceTime.AddDate(0, 0, -days)) {
				return true
			}
		}
		return false
	}
	purchasedWithin := func(id int64, days int) bool {
		for _, event := range dataset.PurchaseEvents {
			if event.UserID == id && !event.OccurredAt.Before(referenceTime.AddDate(0, 0, -days)) {
				return true
			}
		}
		return false
	}

	tests := []struct {
		name  string
		spec  segment.Spec
		match func(seed.User) bool
	}{
		{"city", segment.Spec{City: ptr("Ankara")}, func(u seed.User) bool { return u.City == "Ankara" }},
		{"language", segment.Spec{Language: ptr("en")}, func(u seed.User) bool { return u.Language == "en" }},
		{"cart window", segment.Spec{CartAddedWithinDays: ptr(14)}, func(u seed.User) bool { return cartWithin(u.ID, 14) }},
		{"purchased window", segment.Spec{PurchasedWithinDays: ptr(30)}, func(u seed.User) bool { return purchasedWithin(u.ID, 30) }},
		{"not purchased window", segment.Spec{NotPurchasedWithinDays: ptr(30)}, func(u seed.User) bool { return !purchasedWithin(u.ID, 30) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := 0
			for _, user := range dataset.Users {
				if tt.match(user) {
					want++
				}
			}
			counts, err := segment.Count(ctx, pool, tt.spec, referenceTime)
			if err != nil {
				t.Fatalf("count: %v", err)
			}
			if counts.Total != want {
				t.Fatalf("got %d, want %d", counts.Total, want)
			}
		})
	}
}

// An empty segment must never mean "everyone".
func TestNarrowCountsEachConditionInTurn(t *testing.T) {
	t.Parallel()
	pool, dataset := seededDatabase(t, 200)
	ctx := context.Background()
	spec := exampleSpec()

	breakdown, err := segment.Narrow(ctx, pool, spec, referenceTime)
	if err != nil {
		t.Fatal(err)
	}
	if breakdown.Everyone != len(dataset.Users) {
		t.Fatalf("everyone: got %d, want %d", breakdown.Everyone, len(dataset.Users))
	}
	wantFields := []string{"city", "cart_added_within_days", "no_purchase_since_cart"}
	partial := []segment.Spec{
		{City: spec.City},
		{City: spec.City, CartAddedWithinDays: spec.CartAddedWithinDays},
		spec,
	}
	if len(breakdown.Steps) != len(wantFields) {
		t.Fatalf("steps: %+v", breakdown.Steps)
	}
	for i, step := range breakdown.Steps {
		counts, err := segment.Count(ctx, pool, partial[i], referenceTime)
		if err != nil {
			t.Fatal(err)
		}
		if step.Field != wantFields[i] || step.Remaining != counts.Total {
			t.Fatalf("step %d: got %+v, want %s with %d", i, step, wantFields[i], counts.Total)
		}
	}
	if !(breakdown.Steps[0].Remaining > breakdown.Steps[1].Remaining && breakdown.Steps[1].Remaining > breakdown.Steps[2].Remaining) {
		t.Fatalf("each condition should narrow this audience: %+v", breakdown.Steps)
	}
}

func TestEmptySpecMatchesNobody(t *testing.T) {
	t.Parallel()
	pool, _ := seededDatabase(t, 50)
	ctx := context.Background()

	counts, err := segment.Count(ctx, pool, segment.Spec{}, referenceTime)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts != (segment.Counts{}) {
		t.Fatalf("empty spec matched users: %+v", counts)
	}

	ids, err := segment.MatchingUserIDs(ctx, pool, segment.Spec{}, referenceTime)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("empty spec matched %d users", len(ids))
	}
}

func TestAudienceSkipsPeopleMessagedRecently(t *testing.T) {
	t.Parallel()
	pool, _ := seededDatabase(t, 13)
	ctx := context.Background()
	var campaignID, versionID string
	if err := pool.QueryRow(ctx, `INSERT INTO campaigns (brief, name) VALUES ('other', 'other') RETURNING id::text`).Scan(&campaignID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO campaign_versions (campaign_id, version, segment, content) VALUES ($1, 1, '{}', '{}') RETURNING id::text`, campaignID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	deliver := func(user int64, channel, status string, at time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			WITH run AS (
				INSERT INTO campaign_runs (campaign_id, number)
				SELECT $1, COALESCE(max(number), 0) + 1 FROM campaign_runs WHERE campaign_id = $1
				RETURNING id
			)
			INSERT INTO campaign_deliveries (campaign_id, run_id, campaign_version_id, user_id, channel, idempotency_key, status, updated_at)
			SELECT $1, run.id, $2, $3, $4, gen_random_uuid()::text, $5, $6 FROM run`, campaignID, versionID, user, channel, status, at); err != nil {
			t.Fatal(err)
		}
	}
	base := exampleSpec()
	before, err := segment.Count(ctx, pool, base, referenceTime)
	if err != nil {
		t.Fatal(err)
	}
	spec := base
	spec.NotMessagedWithinDays = ptr(7)
	count := func() int {
		t.Helper()
		got, err := segment.Count(ctx, pool, spec, referenceTime)
		if err != nil {
			t.Fatal(err)
		}
		return got.Total
	}
	if got := count(); got != before.Total {
		t.Fatalf("nobody was messaged yet: %d, want %d", got, before.Total)
	}

	matching, err := segment.MatchingUserIDs(ctx, pool, base, referenceTime)
	if err != nil || len(matching) < 6 {
		t.Fatalf("matching: %d %v", len(matching), err)
	}
	deliver(matching[0], "sms", "SENT", referenceTime.Add(-24*time.Hour))
	deliver(matching[1], "email", "UNKNOWN", referenceTime.Add(-24*time.Hour))
	if got := count(); got != before.Total-2 {
		t.Fatalf("a sent message and a possibly sent one both count: %d", got)
	}

	// None of these reached the person within the window.
	deliver(matching[2], "sms", "SENT", referenceTime.AddDate(0, 0, -8))
	deliver(matching[3], "sms", "FAILED", referenceTime.Add(-24*time.Hour))
	deliver(matching[4], "sms", "SKIPPED", referenceTime.Add(-24*time.Hour))
	// Sent after the audience was counted, as this campaign's own messages are, so
	// the number under review does not fall as the campaign goes out.
	deliver(matching[5], "sms", "SENT", referenceTime.Add(time.Hour))
	if got := count(); got != before.Total-2 {
		t.Fatalf("only recent messages exclude someone: %d", got)
	}
}
