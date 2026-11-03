package segment

import (
	"context"
	"fmt"
	"strings"
	"time"

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
