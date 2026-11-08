package campaign

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

const analyticsCampaigns = 20

// analyticsRanges are the only windows on offer, in days. Calls are read by day, so
// the number of bars stays small enough to read.
var analyticsRanges = []int{7, 30, 90}

// ChannelTotals is what happened on one channel, by when a delivery was created.
// Delivered, bounced and waiting together make up accepted.
type ChannelTotals struct {
	Channel   string `json:"channel"`
	Planned   int    `json:"planned"`
	Skipped   int    `json:"skipped"`
	Accepted  int    `json:"accepted"`
	Failed    int    `json:"failed"`
	Unknown   int    `json:"unknown"`
	OnTheWay  int    `json:"on_the_way"`
	Delivered int    `json:"delivered"`
	Bounced   int    `json:"bounced"`
	Waiting   int    `json:"waiting"`
	Opened    int    `json:"opened"`
}

// CallDay is the provider calls made on one day, in UTC.
type CallDay struct {
	Day         time.Time `json:"day"`
	Accepted    int       `json:"accepted"`
	NotAccepted int       `json:"not_accepted"`
}

// CampaignResult is one campaign's result inside the window.
type CampaignResult struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Accepted int    `json:"accepted"`
	Failed   int    `json:"failed"`
	Unknown  int    `json:"unknown"`
}

// Analytics compares every campaign over a window.
type Analytics struct {
	Days      int              `json:"days"`
	Channels  []ChannelTotals  `json:"channels"`
	Calls     []CallDay        `json:"calls"`
	Campaigns []CampaignResult `json:"campaigns"`
}

// Analytics reads the results of all campaigns over the last days.
func (s *Service) Analytics(ctx context.Context, days int) (Analytics, error) {
	if !slices.Contains(analyticsRanges, days) {
		return Analytics{}, ErrUnknownFilter
	}
	since := s.now().AddDate(0, 0, -days)
	result := Analytics{Days: days}

	// Arrival is read in one pass over the window's events and joined once, as the
	// report does: a lookup per delivery made this query grow with every delivery
	// in the window (docs/performance.md, step 9). An event is received after its
	// delivery was created, so received_at bounds the events without missing one.
	rows, err := s.db.Query(ctx, `
		WITH arrival AS (
			SELECT delivery_id,
			       max(occurred_at) FILTER (WHERE event_type = 'bounced') AS bounced_at,
			       CASE WHEN count(*) FILTER (WHERE event_type = 'bounced') = 0
			            THEN min(occurred_at) FILTER (WHERE event_type IN ('delivered', 'opened')) END AS delivered_at,
			       bool_or(event_type = 'opened') AS opened
			FROM delivery_events WHERE received_at >= $1
			GROUP BY delivery_id
		)
		SELECT d.channel, count(*),
		       count(*) FILTER (WHERE d.status = 'SKIPPED'),
		       count(*) FILTER (WHERE d.status = 'SENT'),
		       count(*) FILTER (WHERE d.status = 'FAILED'),
		       count(*) FILTER (WHERE d.status = 'UNKNOWN'),
		       count(*) FILTER (WHERE d.status IN ('QUEUED', 'SENDING', 'RETRYING')),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.delivered_at IS NOT NULL),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.bounced_at IS NOT NULL),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.delivered_at IS NULL AND arrival.bounced_at IS NULL),
		       count(*) FILTER (WHERE arrival.opened)
		FROM campaign_deliveries d
		LEFT JOIN arrival ON arrival.delivery_id = d.id
		WHERE d.created_at >= $1
		GROUP BY d.channel ORDER BY d.channel`, since)
	if err != nil {
		return Analytics{}, fmt.Errorf("channel totals: %w", err)
	}
	if result.Channels, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ChannelTotals]); err != nil {
		return Analytics{}, fmt.Errorf("channel totals: %w", err)
	}

	rows, err = s.db.Query(ctx, `
		SELECT date_trunc('day', occurred_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
		       count(*) FILTER (WHERE outcome = 'accepted'),
		       count(*) FILTER (WHERE outcome <> 'accepted')
		FROM delivery_attempts WHERE occurred_at >= $1
		GROUP BY 1 ORDER BY 1`, since)
	if err != nil {
		return Analytics{}, fmt.Errorf("calls by day: %w", err)
	}
	if result.Calls, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CallDay]); err != nil {
		return Analytics{}, fmt.Errorf("calls by day: %w", err)
	}

	// Worst first: this table is for finding what to look at.
	rows, err = s.db.Query(ctx, `
		SELECT c.id::text, c.name, c.status, r.accepted, r.failed, r.unknown
		FROM (
			SELECT campaign_id,
			       count(*) FILTER (WHERE status = 'SENT') AS accepted,
			       count(*) FILTER (WHERE status = 'FAILED') AS failed,
			       count(*) FILTER (WHERE status = 'UNKNOWN') AS unknown
			FROM campaign_deliveries WHERE created_at >= $1
			GROUP BY campaign_id
		) r
		JOIN campaigns c ON c.id = r.campaign_id
		WHERE r.accepted + r.failed + r.unknown > 0
		ORDER BY (r.failed + r.unknown)::float / (r.accepted + r.failed + r.unknown) DESC, r.accepted + r.failed + r.unknown DESC, c.id
		LIMIT $2`, since, analyticsCampaigns)
	if err != nil {
		return Analytics{}, fmt.Errorf("campaign results: %w", err)
	}
	if result.Campaigns, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CampaignResult]); err != nil {
		return Analytics{}, fmt.Errorf("campaign results: %w", err)
	}
	return result, nil
}
