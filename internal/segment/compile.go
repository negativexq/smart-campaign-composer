package segment

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Querier is satisfied by a pool or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Counts is the audience size overall and per consented channel.
type Counts struct {
	Total int `json:"total"`
	SMS   int `json:"sms"`
	Email int `json:"email"`
}

// Compile turns a spec into a WHERE clause over users aliased as u. Only the
// fixed fragments below ever reach the SQL text; every value is a parameter.
// A spec that produces no condition matches nobody, never everyone.
func Compile(spec Spec, now time.Time) (string, []any) {
	var conditions []string
	var args []any
	param := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	cutoff := func(days int) string {
		return param(now.AddDate(0, 0, -days))
	}

	if spec.City != nil {
		conditions = append(conditions, "u.city = "+param(*spec.City))
	}
	if spec.Language != nil {
		conditions = append(conditions, "u.language = "+param(*spec.Language))
	}
	if spec.CartAddedWithinDays != nil {
		sinceCart := ""
		if spec.noPurchaseSinceCart() {
			sinceCart = " AND NOT EXISTS (SELECT 1 FROM purchase_events p WHERE p.user_id = u.id AND p.occurred_at > c.occurred_at)"
		}
		conditions = append(conditions, "EXISTS (SELECT 1 FROM cart_events c WHERE c.user_id = u.id AND c.occurred_at >= "+
			cutoff(*spec.CartAddedWithinDays)+sinceCart+")")
	}
	if spec.PurchasedWithinDays != nil {
		conditions = append(conditions, "EXISTS (SELECT 1 FROM purchase_events p WHERE p.user_id = u.id AND p.occurred_at >= "+
			cutoff(*spec.PurchasedWithinDays)+")")
	}
	if spec.NotPurchasedWithinDays != nil {
		conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM purchase_events p WHERE p.user_id = u.id AND p.occurred_at >= "+
			cutoff(*spec.NotPurchasedWithinDays)+")")
	}
	if spec.NotMessagedWithinDays != nil {
		// Only what went out before now, so a campaign's own messages do not shrink
		// the audience it was approved with.
		conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM campaign_deliveries d WHERE d.user_id = u.id AND d.status IN ('SENT', 'UNKNOWN') AND d.updated_at >= "+
			cutoff(*spec.NotMessagedWithinDays)+" AND d.updated_at < "+param(now)+")")
	}
	if len(conditions) == 0 {
		return "FALSE", nil
	}
	return strings.Join(conditions, " AND "), args
}

// Count sizes the audience. Total includes users without consent; the channel
// counts are who can actually be messaged.
func Count(ctx context.Context, db Querier, spec Spec, now time.Time) (Counts, error) {
	where, args := Compile(spec, now)
	query := `
		SELECT count(*),
		       count(*) FILTER (WHERE sms.allowed),
		       count(*) FILTER (WHERE email.allowed)
		FROM users u
		LEFT JOIN consents sms ON sms.user_id = u.id AND sms.channel = 'sms'
		LEFT JOIN consents email ON email.user_id = u.id AND email.channel = 'email'
		WHERE ` + where

	var counts Counts
	if err := db.QueryRow(ctx, query, args...).Scan(&counts.Total, &counts.SMS, &counts.Email); err != nil {
		return Counts{}, fmt.Errorf("count audience: %w", err)
	}
	return counts, nil
}

// BreakdownStep says how many people are left once a condition and every one
// before it are applied.
type BreakdownStep struct {
	Field     string `json:"field"`
	Remaining int    `json:"remaining"`
}

// Breakdown shows how each condition narrows the customer base, in Describe's order.
type Breakdown struct {
	Everyone int             `json:"everyone"`
	Steps    []BreakdownStep `json:"steps"`
}

// Narrow counts the audience after each condition in turn.
func Narrow(ctx context.Context, db Querier, spec Spec, now time.Time) (Breakdown, error) {
	breakdown := Breakdown{Steps: []BreakdownStep{}}
	if err := db.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&breakdown.Everyone); err != nil {
		return Breakdown{}, fmt.Errorf("count customers: %w", err)
	}
	var applied Spec
	for _, condition := range Describe(spec) {
		switch condition.Field {
		case "city":
			applied.City = spec.City
		case "language":
			applied.Language = spec.Language
		case "cart_added_within_days":
			applied.CartAddedWithinDays = spec.CartAddedWithinDays
		case "no_purchase_since_cart":
			applied.NoPurchaseSinceCart = spec.NoPurchaseSinceCart
		case "purchased_within_days":
			applied.PurchasedWithinDays = spec.PurchasedWithinDays
		case "not_purchased_within_days":
			applied.NotPurchasedWithinDays = spec.NotPurchasedWithinDays
		case "not_messaged_within_days":
			applied.NotMessagedWithinDays = spec.NotMessagedWithinDays
		}
		counts, err := Count(ctx, db, applied, now)
		if err != nil {
			return Breakdown{}, err
		}
		breakdown.Steps = append(breakdown.Steps, BreakdownStep{condition.Field, counts.Total})
	}
	return breakdown, nil
}

// MatchingUserIDs lists the audience, regardless of consent.
func MatchingUserIDs(ctx context.Context, db Querier, spec Spec, now time.Time) ([]int64, error) {
	where, args := Compile(spec, now)
	rows, err := db.Query(ctx, "SELECT u.id FROM users u WHERE "+where+" ORDER BY u.id", args...)
	if err != nil {
		return nil, fmt.Errorf("list audience: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("list audience: %w", err)
	}
	return ids, nil
}

// NameLengths is the longest first name per consented channel, in characters.
type NameLengths struct {
	SMS   int
	Email int
}

// LongestNames tells how long {{first_name}} can become for people who can
// actually receive each channel.
func LongestNames(ctx context.Context, db Querier, spec Spec, now time.Time) (NameLengths, error) {
	where, args := Compile(spec, now)
	query := `
		SELECT COALESCE(max(char_length(u.first_name)) FILTER (WHERE sms.allowed), 0),
		       COALESCE(max(char_length(u.first_name)) FILTER (WHERE email.allowed), 0)
		FROM users u
		LEFT JOIN consents sms ON sms.user_id = u.id AND sms.channel = 'sms'
		LEFT JOIN consents email ON email.user_id = u.id AND email.channel = 'email'
		WHERE ` + where

	var lengths NameLengths
	if err := db.QueryRow(ctx, query, args...).Scan(&lengths.SMS, &lengths.Email); err != nil {
		return NameLengths{}, fmt.Errorf("longest names: %w", err)
	}
	return lengths, nil
}

// Sample is an audience member shown in a message preview.
type Sample struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	FirstName string `json:"first_name"`
}

// SampleRecipients picks a typical person and the names most likely to break a
// message: the longest and a missing one. Only people who can receive a message
// are considered.
func SampleRecipients(ctx context.Context, db Querier, spec Spec, now time.Time) ([]Sample, error) {
	where, args := Compile(spec, now)
	rows, err := db.Query(ctx, `
		SELECT u.id, u.first_name
		FROM users u
		WHERE EXISTS (SELECT 1 FROM consents c WHERE c.user_id = u.id AND c.allowed)
		  AND `+where+`
		ORDER BY u.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("sample recipients: %w", err)
	}
	people, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Sample, error) {
		var person Sample
		err := row.Scan(&person.ID, &person.FirstName)
		return person, err
	})
	if err != nil {
		return nil, fmt.Errorf("sample recipients: %w", err)
	}

	var typical, longest, unnamed *Sample
	for i := range people {
		person := &people[i]
		length := utf8.RuneCountInString(person.FirstName)
		switch {
		case length == 0:
			if unnamed == nil {
				unnamed = person
			}
		case longest == nil || length > utf8.RuneCountInString(longest.FirstName):
			if typical == nil {
				typical = longest
			}
			longest = person
		case typical == nil:
			typical = person
		}
	}

	samples := []Sample{}
	if typical != nil {
		samples = append(samples, Sample{typical.ID, typical.FirstName, typical.FirstName})
	}
	if longest != nil {
		label := "Longest name: " + longest.FirstName
		samples = append(samples, Sample{longest.ID, label, longest.FirstName})
	}
	if unnamed != nil {
		samples = append(samples, Sample{unnamed.ID, "No first name", ""})
	}
	return samples, nil
}
