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
	// Only fixtures, so the expected numbers are exact: 9 matching İstanbul users
	// minus none, of which one has no consent, one is sms-only, one is email-only.
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
