package campaign

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// monitorStatuses are the deliveries someone running sends looks after: those still
// on their way and those that ended badly. Accepted and skipped ones need no one.
var monitorStatuses = []string{"QUEUED", "SENDING", "RETRYING", "UNKNOWN", "FAILED"}

// MonitorFilter narrows the delivery monitor. Empty values mean no filter.
type MonitorFilter struct {
	Status  string
	Channel string
	// Outcome keeps deliveries with a provider call that ended this way.
	Outcome string
	// Reason keeps deliveries whose last problem was this.
	Reason string
	// WithinHours keeps deliveries that changed that recently, and calls made that recently.
	WithinHours int
	Limit       int
	Offset      int
}

// MonitorEntry is one delivery with the campaign it belongs to.
type MonitorEntry struct {
	ID            string    `json:"id"`
	CampaignID    string    `json:"campaign_id"`
	CampaignName  string    `json:"campaign_name"`
	UserID        int64     `json:"user_id"`
	FirstName     string    `json:"first_name"`
	Channel       string    `json:"channel"`
	Status        string    `json:"status"`
	Calls         int       `json:"calls"`
	ResponseCode  *int      `json:"response_code"`
	Reason        string    `json:"reason"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PausedCampaign is a campaign whose deliveries are waiting for someone to resume it.
type PausedCampaign struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ReasonCount is how many deliveries last had one problem. An empty reason means none yet.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Monitor is the state of sending across every campaign.
type Monitor struct {
	// Counts holds every monitored status, whichever one is selected, so the others
	// can still be seen and chosen.
	Counts map[string]int `json:"counts"`
	// Channels and Reasons hold the same, each leaving out its own filter.
	Channels   map[string]int   `json:"channels"`
	Reasons    []ReasonCount    `json:"reasons"`
	Total      int              `json:"total"`
	Deliveries []MonitorEntry   `json:"deliveries"`
	Paused     []PausedCampaign `json:"paused"`
}

// ReasonNone asks for deliveries that have had no problem so far.
const ReasonNone = "none"

const monitorMatching = `
	FROM campaign_deliveries d
	WHERE d.status = ANY($1) AND ($2 = '' OR d.channel = $2)
	  AND ($4::timestamptz IS NULL OR d.updated_at >= $4)
	  AND ($5 = '' OR d.last_error = $5 OR ($5 = '` + ReasonNone + `' AND COALESCE(d.last_error, '') = ''))
	  AND ($3 = '' OR EXISTS (
		SELECT 1 FROM delivery_attempts a
		WHERE a.delivery_id = d.id AND a.outcome = $3 AND ($4::timestamptz IS NULL OR a.occurred_at >= $4)
	  ))`

// DeliveryMonitor reads the deliveries that need watching, most recently changed first.
func (s *Service) DeliveryMonitor(ctx context.Context, filter MonitorFilter) (Monitor, error) {
	if filter.Status != "" && !slices.Contains(monitorStatuses, filter.Status) {
		return Monitor{}, ErrUnknownFilter
	}
	if filter.Channel != "" && !slices.Contains(channels, filter.Channel) {
		return Monitor{}, ErrUnknownFilter
	}
	if filter.Outcome != "" && !slices.Contains(attemptOutcomes, filter.Outcome) {
		return Monitor{}, ErrUnknownFilter
	}
	if filter.WithinHours < 0 {
		return Monitor{}, ErrUnknownFilter
	}
	var since *time.Time
	if filter.WithinHours > 0 {
		at := s.now().Add(-time.Duration(filter.WithinHours) * time.Hour)
		since = &at
	}

	selected := monitorStatuses
	if filter.Status != "" {
		selected = []string{filter.Status}
	}
	monitor := Monitor{Counts: make(map[string]int, len(monitorStatuses)), Channels: make(map[string]int, len(channels))}
	for _, channel := range channels {
		monitor.Channels[channel] = 0
	}
	for _, status := range monitorStatuses {
		monitor.Counts[status] = 0
	}
	rows, err := s.db.Query(ctx, `SELECT d.status, count(*)`+monitorMatching+` GROUP BY d.status`, monitorStatuses, filter.Channel, filter.Outcome, since, filter.Reason)
	if err != nil {
		return Monitor{}, fmt.Errorf("count monitored deliveries: %w", err)
	}
	var (
		status string
		count  int
	)
	if _, err := pgx.ForEachRow(rows, []any{&status, &count}, func() error {
		monitor.Counts[status] = count
		if filter.Status == "" || filter.Status == status {
			monitor.Total += count
		}
		return nil
	}); err != nil {
		return Monitor{}, fmt.Errorf("count monitored deliveries: %w", err)
	}

	var channel string
	rows, err = s.db.Query(ctx, `SELECT d.channel, count(*)`+monitorMatching+` GROUP BY d.channel`, selected, "", filter.Outcome, since, filter.Reason)
	if err != nil {
		return Monitor{}, fmt.Errorf("count monitored channels: %w", err)
	}
	if _, err := pgx.ForEachRow(rows, []any{&channel, &count}, func() error {
		monitor.Channels[channel] = count
		return nil
	}); err != nil {
		return Monitor{}, fmt.Errorf("count monitored channels: %w", err)
	}
	rows, err = s.db.Query(ctx, `SELECT COALESCE(d.last_error, ''), count(*)`+monitorMatching+` GROUP BY 1 ORDER BY 2 DESC, 1`, selected, filter.Channel, filter.Outcome, since, "")
	if err != nil {
		return Monitor{}, fmt.Errorf("count monitored reasons: %w", err)
	}
	if monitor.Reasons, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ReasonCount]); err != nil {
		return Monitor{}, fmt.Errorf("count monitored reasons: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultDeliveryLimit
	}
	limit = min(limit, maxDeliveryLimit)
	rows, err = s.db.Query(ctx, `
		SELECT d.id::text, d.campaign_id::text, c.name, d.user_id, u.first_name, d.channel, d.status,
		       (SELECT count(*) FROM delivery_attempts a WHERE a.delivery_id = d.id), last.response_code,
		       COALESCE(d.last_error, ''), d.next_attempt_at, d.updated_at
		FROM (SELECT d.id`+monitorMatching+`
			ORDER BY d.updated_at DESC, d.id
			LIMIT $6 OFFSET $7) page
		JOIN campaign_deliveries d ON d.id = page.id
		JOIN campaigns c ON c.id = d.campaign_id
		JOIN users u ON u.id = d.user_id
		LEFT JOIN LATERAL (
			SELECT response_code FROM delivery_attempts WHERE delivery_id = d.id ORDER BY id DESC LIMIT 1
		) last ON true
		ORDER BY d.updated_at DESC, d.id`, selected, filter.Channel, filter.Outcome, since, filter.Reason, limit, max(filter.Offset, 0))
	if err != nil {
		return Monitor{}, fmt.Errorf("list monitored deliveries: %w", err)
	}
	if monitor.Deliveries, err = pgx.CollectRows(rows, pgx.RowToStructByPos[MonitorEntry]); err != nil {
		return Monitor{}, fmt.Errorf("list monitored deliveries: %w", err)
	}

	rows, err = s.db.Query(ctx, `SELECT id::text, name FROM campaigns WHERE status = $1 ORDER BY updated_at DESC`, statusPaused)
	if err != nil {
		return Monitor{}, fmt.Errorf("list paused campaigns: %w", err)
	}
	if monitor.Paused, err = pgx.CollectRows(rows, pgx.RowToStructByPos[PausedCampaign]); err != nil {
		return Monitor{}, fmt.Errorf("list paused campaigns: %w", err)
	}
	return monitor, nil
}
