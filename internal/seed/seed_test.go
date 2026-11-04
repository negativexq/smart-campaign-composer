package seed_test

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/language"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

var referenceTime = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// exampleBriefAudience answers the example brief in plain Go: users in Istanbul
// with a cart event in the last 30 days and no purchase after that cart event.
func exampleBriefAudience(ds seed.Dataset, now time.Time) []int64 {
	lastPurchase := map[int64]time.Time{}
	for _, p := range ds.PurchaseEvents {
		if p.OccurredAt.After(lastPurchase[p.UserID]) {
			lastPurchase[p.UserID] = p.OccurredAt
		}
	}
	matched := map[int64]bool{}
	for _, c := range ds.CartEvents {
		if !c.OccurredAt.Before(now.Add(-30*day)) && !lastPurchase[c.UserID].After(c.OccurredAt) {
			matched[c.UserID] = true
		}
	}
	var ids []int64
	for _, u := range ds.Users {
		if u.City == seed.CityIstanbul && matched[u.ID] {
			ids = append(ids, u.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

func TestGenerateIsDeterministic(t *testing.T) {
	t.Parallel()
	first := seed.Generate(referenceTime, 500)
	second := seed.Generate(referenceTime, 500)

	if !reflect.DeepEqual(first, second) {
		t.Fatal("same reference time and size must produce identical datasets")
	}
}

func TestGenerateProducesRequestedUserCountWithUniqueContacts(t *testing.T) {
	t.Parallel()
	ds := seed.Generate(referenceTime, seed.DefaultUserCount)

	if len(ds.Users) != seed.DefaultUserCount {
		t.Fatalf("want %d users, got %d", seed.DefaultUserCount, len(ds.Users))
	}
	ids, emails, phones := map[int64]bool{}, map[string]bool{}, map[string]bool{}
	for _, u := range ds.Users {
		if ids[u.ID] || emails[u.Email] || phones[u.Phone] {
			t.Fatalf("duplicate identity for user %d", u.ID)
		}
		ids[u.ID], emails[u.Email], phones[u.Phone] = true, true, true
	}
}

// Timestamps must follow the seed time. With fixed dates, a reviewer starting
// the system weeks later would see the 30-day window empty out.
func TestEventsAreRelativeToReferenceTime(t *testing.T) {
	t.Parallel()
	later := referenceTime.Add(90 * day)
	base := seed.Generate(referenceTime, 500)
	shifted := seed.Generate(later, 500)

	if len(base.CartEvents) == 0 || len(base.PurchaseEvents) == 0 {
		t.Fatal("dataset must contain cart and purchase events")
	}
	for i, event := range base.CartEvents {
		if got := shifted.CartEvents[i].OccurredAt.Sub(event.OccurredAt); got != 90*day {
			t.Fatalf("cart event %d moved by %v, want %v", i, got, 90*day)
		}
	}
	for _, event := range append(slices.Clone(base.CartEvents), base.PurchaseEvents...) {
		if event.OccurredAt.After(referenceTime) {
			t.Fatalf("event for user %d is in the future: %v", event.UserID, event.OccurredAt)
		}
	}
	if got, want := exampleBriefAudience(shifted, later), exampleBriefAudience(base, referenceTime); !slices.Equal(got, want) {
		t.Fatalf("audience changed with the reference time: %d vs %d users", len(got), len(want))
	}
}

func TestFixturesCoverExampleBriefEdgeCases(t *testing.T) {
	t.Parallel()
	ds := seed.Generate(referenceTime, 100)
	audience := exampleBriefAudience(ds, referenceTime)

	tests := []struct {
		name  string
		id    int64
		match bool
	}{
		{"cart without purchase matches", seed.FixtureCartNoPurchase, true},
		{"purchase after cart is excluded", seed.FixturePurchasedAfterCart, false},
		{"purchase before cart still matches", seed.FixturePurchaseBeforeCart, true},
		{"cart outside the window is excluded", seed.FixtureCartTooOld, false},
		{"other city is excluded", seed.FixtureOtherCity, false},
		{"later cart after a purchase matches", seed.FixtureCartAgainAfterPurchase, true},
		{"purchase after the only cart is excluded", seed.FixtureOldCartThenPurchase, false},
		{"sms-only consent still matches the segment", seed.FixtureSMSOnly, true},
		{"email-only consent still matches the segment", seed.FixtureEmailOnly, true},
		{"no consent still matches the segment", seed.FixtureNoConsent, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slices.Contains(audience, tt.id); got != tt.match {
				t.Errorf("user %d: matched=%v, want %v", tt.id, got, tt.match)
			}
		})
	}
}

func TestFixturesCoverConsentAndNameEdgeCases(t *testing.T) {
	t.Parallel()
	ds := seed.Generate(referenceTime, 100)
	users := map[int64]seed.User{}
	for _, u := range ds.Users {
		users[u.ID] = u
	}

	if u := users[seed.FixtureSMSOnly]; !u.SMSAllowed || u.EmailAllowed {
		t.Errorf("sms-only fixture has wrong consent: %+v", u)
	}
	if u := users[seed.FixtureEmailOnly]; u.SMSAllowed || !u.EmailAllowed {
		t.Errorf("email-only fixture has wrong consent: %+v", u)
	}
	if u := users[seed.FixtureNoConsent]; u.SMSAllowed || u.EmailAllowed {
		t.Errorf("no-consent fixture has wrong consent: %+v", u)
	}
	if u := users[seed.FixtureLongName]; len([]rune(u.FirstName)) < 20 {
		t.Errorf("long-name fixture is too short for limit tests: %q", u.FirstName)
	}
	if u := users[seed.FixtureEmptyName]; u.FirstName != "" {
		t.Errorf("empty-name fixture has a name: %q", u.FirstName)
	}
	if u := users[seed.FixtureTurkishName]; u.FirstName != "Şükrü" {
		t.Errorf("turkish-name fixture changed: %q", u.FirstName)
	}
}

func TestApplyLoadsDatasetOnce(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	ctx := context.Background()
	ds := seed.Generate(referenceTime, 200)

	applied, err := seed.Apply(ctx, pool, ds)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !applied {
		t.Fatal("first apply must load the dataset")
	}

	applied, err = seed.Apply(ctx, pool, seed.Generate(referenceTime.Add(day), 300))
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if applied {
		t.Fatal("second apply must leave existing data untouched")
	}

	for table, want := range map[string]int{
		"users":           len(ds.Users),
		"consents":        len(ds.Users) * 2,
		"cart_events":     len(ds.CartEvents),
		"purchase_events": len(ds.PurchaseEvents),
	} {
		var got int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s: got %d rows, want %d", table, got, want)
		}
	}
}

func TestLoadedDataAnswersExampleBriefLikeTheDataset(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	ctx := context.Background()
	ds := seed.Generate(referenceTime, 500)
	if _, err := seed.Apply(ctx, pool, ds); err != nil {
		t.Fatalf("apply: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT u.id FROM users u
		WHERE u.city = $1 AND EXISTS (
			SELECT 1 FROM cart_events c
			WHERE c.user_id = u.id AND c.occurred_at >= $2::timestamptz
			AND NOT EXISTS (
				SELECT 1 FROM purchase_events p
				WHERE p.user_id = u.id AND p.occurred_at > c.occurred_at
			)
		)
		ORDER BY u.id`, seed.CityIstanbul, referenceTime.Add(-30*day))
	if err != nil {
		t.Fatalf("query audience: %v", err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := exampleBriefAudience(ds, referenceTime)
	if len(want) == 0 {
		t.Fatal("example brief must match some users")
	}
	if !slices.Equal(got, want) {
		t.Fatalf("sql audience (%d users) differs from dataset audience (%d users)", len(got), len(want))
	}
}

func TestPeopleReadEveryLanguageWeWriteIn(t *testing.T) {
	t.Parallel()
	counts := map[string]int{}
	for _, user := range seed.Generate(referenceTime, seed.DefaultUserCount).Users {
		counts[user.Language]++
	}
	for _, code := range language.Codes() {
		if counts[code] < 100 {
			t.Fatalf("%s has %d readers: %v", code, counts[code], counts)
		}
	}
	if len(counts) != len(language.Codes()) {
		t.Fatalf("readers of a language we cannot write in: %v", counts)
	}
}
