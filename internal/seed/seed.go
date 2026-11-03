// Package seed generates and loads deterministic fake users. All event times
// are offsets from a reference time, so segments like "last 30 days" keep
// matching the same users no matter when the system is started.
package seed

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// DefaultUserCount is the size of the demo dataset.
	DefaultUserCount = 5000

	// CityIstanbul is the canonical spelling segments must resolve to.
	CityIstanbul = "İstanbul"

	day = 24 * time.Hour
)

// Fixture users have fixed IDs and exact behaviour for tests and demos.
// Generated users start after the last fixture.
const (
	FixtureCartNoPurchase int64 = iota + 1
	FixturePurchasedAfterCart
	FixturePurchaseBeforeCart
	FixtureCartTooOld
	FixtureOtherCity
	FixtureCartAgainAfterPurchase
	FixtureOldCartThenPurchase
	FixtureSMSOnly
	FixtureEmailOnly
	FixtureNoConsent
	FixtureLongName
	FixtureEmptyName
	FixtureTurkishName
	lastFixture = FixtureTurkishName
)

// Cities lists every city in the dataset using its canonical spelling.
var Cities = []string{CityIstanbul, "Ankara", "İzmir", "Bursa", "Antalya", "Adana"}

var firstNames = []string{
	"Ayşe", "Mehmet", "Fatma", "Ahmet", "Zeynep", "Mustafa", "Elif", "Can",
	"Deniz", "Ece", "Emre", "Selin", "Burak", "Merve", "Ömer", "Gül",
}

// User is a fake recipient with per-channel consent.
type User struct {
	ID           int64
	FirstName    string
	City         string
	Email        string
	Phone        string
	Language     string
	SMSAllowed   bool
	EmailAllowed bool
}

// Event is a cart or purchase event.
type Event struct {
	UserID     int64
	ProductID  int64
	OccurredAt time.Time
}

// Dataset is everything the seed loads.
type Dataset struct {
	Users          []User
	CartEvents     []Event
	PurchaseEvents []Event
}

// Generate builds the dataset. The same reference time and size always produce
// the same result. userCount must be large enough to hold the fixtures.
func Generate(now time.Time, userCount int) Dataset {
	now = now.UTC().Truncate(time.Second)
	b := builder{now: now}
	b.addFixtures()

	rng := rand.New(rand.NewPCG(20261103, 1))
	for id := lastFixture + 1; id <= int64(userCount); id++ {
		b.addGeneratedUser(rng, id)
	}
	return b.ds
}

type builder struct {
	now time.Time
	ds  Dataset
}

func (b *builder) addUser(id int64, firstName, city string, smsAllowed, emailAllowed bool, language string) {
	b.ds.Users = append(b.ds.Users, User{
		ID:           id,
		FirstName:    firstName,
		City:         city,
		Email:        fmt.Sprintf("user%d@example.test", id),
		Phone:        fmt.Sprintf("+90500%07d", id),
		Language:     language,
		SMSAllowed:   smsAllowed,
		EmailAllowed: emailAllowed,
	})
}

func (b *builder) cart(id int64, daysAgo int) {
	b.ds.CartEvents = append(b.ds.CartEvents, Event{id, 100 + id, b.now.Add(-time.Duration(daysAgo) * day)})
}

func (b *builder) purchase(id int64, daysAgo int) {
	b.ds.PurchaseEvents = append(b.ds.PurchaseEvents, Event{id, 100 + id, b.now.Add(-time.Duration(daysAgo) * day)})
}

func (b *builder) addFixtures() {
	b.addUser(FixtureCartNoPurchase, "Ayşe", CityIstanbul, true, true, "tr")
	b.cart(FixtureCartNoPurchase, 5)

	b.addUser(FixturePurchasedAfterCart, "Mehmet", CityIstanbul, true, true, "tr")
	b.cart(FixturePurchasedAfterCart, 5)
	b.purchase(FixturePurchasedAfterCart, 2)

	b.addUser(FixturePurchaseBeforeCart, "Fatma", CityIstanbul, true, true, "tr")
	b.purchase(FixturePurchaseBeforeCart, 10)
	b.cart(FixturePurchaseBeforeCart, 5)

	b.addUser(FixtureCartTooOld, "Ahmet", CityIstanbul, true, true, "tr")
	b.cart(FixtureCartTooOld, 40)

	b.addUser(FixtureOtherCity, "Zeynep", "Ankara", true, true, "tr")
	b.cart(FixtureOtherCity, 5)

	b.addUser(FixtureCartAgainAfterPurchase, "Mustafa", CityIstanbul, true, true, "tr")
	b.cart(FixtureCartAgainAfterPurchase, 20)
	b.purchase(FixtureCartAgainAfterPurchase, 10)
	b.cart(FixtureCartAgainAfterPurchase, 3)

	b.addUser(FixtureOldCartThenPurchase, "Elif", CityIstanbul, true, true, "tr")
	b.cart(FixtureOldCartThenPurchase, 20)
	b.purchase(FixtureOldCartThenPurchase, 10)

	b.addUser(FixtureSMSOnly, "Can", CityIstanbul, true, false, "tr")
	b.cart(FixtureSMSOnly, 5)

	b.addUser(FixtureEmailOnly, "Deniz", CityIstanbul, false, true, "tr")
	b.cart(FixtureEmailOnly, 5)

	b.addUser(FixtureNoConsent, "Ece", CityIstanbul, false, false, "tr")
	b.cart(FixtureNoConsent, 5)

	b.addUser(FixtureLongName, "Maximiliano Alexander", CityIstanbul, true, true, "en")
	b.cart(FixtureLongName, 5)

	b.addUser(FixtureEmptyName, "", CityIstanbul, true, true, "tr")
	b.cart(FixtureEmptyName, 5)

	b.addUser(FixtureTurkishName, "Şükrü", CityIstanbul, true, true, "tr")
	b.cart(FixtureTurkishName, 5)
}

func (b *builder) addGeneratedUser(rng *rand.Rand, id int64) {
	city := Cities[1+rng.IntN(len(Cities)-1)]
	if rng.Float64() < 0.35 {
		city = CityIstanbul
	}
	language := "tr"
	if rng.Float64() < 0.15 {
		language = "en"
	}
	name := firstNames[rng.IntN(len(firstNames))]
	b.addUser(id, name, city, rng.Float64() < 0.7, rng.Float64() < 0.8, language)

	if rng.Float64() < 0.4 {
		for range 1 + rng.IntN(3) {
			b.cart(id, rng.IntN(60))
		}
	}
	if rng.Float64() < 0.3 {
		for range 1 + rng.IntN(3) {
			b.purchase(id, rng.IntN(60))
		}
	}
}

// Apply loads the dataset into an empty database and reports whether it did.
// A database that already has users is left alone, so restarts keep campaigns.
func Apply(ctx context.Context, pool *pgxpool.Pool, ds Dataset) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin seed transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Two setup containers starting together must not both load the data.
	if _, err := tx.Exec(ctx, `LOCK TABLE users IN EXCLUSIVE MODE`); err != nil {
		return false, fmt.Errorf("lock users: %w", err)
	}
	var seeded bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&seeded); err != nil {
		return false, fmt.Errorf("check existing users: %w", err)
	}
	if seeded {
		return false, nil
	}

	users := make([][]any, 0, len(ds.Users))
	consents := make([][]any, 0, len(ds.Users)*2)
	for _, u := range ds.Users {
		users = append(users, []any{u.ID, u.FirstName, u.City, u.Email, u.Phone, u.Language})
		consents = append(consents, []any{u.ID, "sms", u.SMSAllowed}, []any{u.ID, "email", u.EmailAllowed})
	}
	copies := []struct {
		table   string
		columns []string
		rows    [][]any
	}{
		{"users", []string{"id", "first_name", "city", "email", "phone", "language"}, users},
		{"consents", []string{"user_id", "channel", "allowed"}, consents},
		{"cart_events", eventColumns, eventRows(ds.CartEvents)},
		{"purchase_events", eventColumns, eventRows(ds.PurchaseEvents)},
	}
	for _, c := range copies {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{c.table}, c.columns, pgx.CopyFromRows(c.rows)); err != nil {
			return false, fmt.Errorf("load %s: %w", c.table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit seed: %w", err)
	}
	return true, nil
}

var eventColumns = []string{"user_id", "product_id", "occurred_at"}

func eventRows(events []Event) [][]any {
	rows := make([][]any, 0, len(events))
	for _, e := range events {
		rows = append(rows, []any{e.UserID, e.ProductID, e.OccurredAt})
	}
	return rows
}
