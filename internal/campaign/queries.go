package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// DefaultListLimit caps the campaign list.
	DefaultListLimit = 100

	recentCampaigns = 5
	recentActivity  = 8
)

// groupStatuses defines the list filters. Each status belongs to at most one
// group, so overview counts add up; cancelled campaigns only appear under all.
var groupStatuses = map[string][]string{
	"drafts":    {"DRAFT", "GENERATING", "READY_FOR_REVIEW"},
	"active":    {"APPROVED", "SCHEDULED", "DISPATCHING"},
	"completed": {"COMPLETED"},
	"failed":    {"GENERATION_FAILED", "COMPLETED_WITH_FAILURES"},
}

// ErrUnknownGroup means a list filter does not exist.
var ErrUnknownGroup = errors.New("unknown campaign group")

// ListFilter narrows the campaign list. An empty Group means all campaigns.
type ListFilter struct {
	Group             string
	Query             string
	Channel           string
	CreatedWithinDays int
	Sort              string
	Limit             int
}

// listOrders holds the only orderings a list can ask for, so no request text
// reaches the query.
var listOrders = map[string]string{
	"":       "c.created_at DESC, c.id",
	"newest": "c.created_at DESC, c.id",
	"oldest": "c.created_at, c.id",
	"name":   "lower(c.name), c.id",
}

// Summary is a campaign row. Progress comes from its deliveries, so it stays empty
// until the campaign is dispatched.
type Summary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Brief     string    `json:"brief"`
	Status    string    `json:"status"`
	Channels  []string  `json:"channels"`
	Total     int       `json:"deliveries_total"`
	Processed int       `json:"deliveries_processed"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// List returns campaigns, newest first.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Summary, error) {
	var statuses []string
	if filter.Group != "" {
		known, ok := groupStatuses[filter.Group]
		if !ok {
			return nil, ErrUnknownGroup
		}
		statuses = known
	}
	order, known := listOrders[filter.Sort]
	if !known || filter.CreatedWithinDays < 0 || (filter.Channel != "" && filter.Channel != "sms" && filter.Channel != "email") {
		return nil, ErrUnknownFilter
	}
	limit := filter.Limit
	if limit <= 0 || limit > DefaultListLimit {
		limit = DefaultListLimit
	}

	rows, err := s.db.Query(ctx, `
		SELECT c.id::text, c.name, c.brief, c.status,
		       c.channels, COALESCE(d.total, 0), COALESCE(d.processed, 0),
		       c.created_at, c.updated_at
		-- Choosing the page first means only its campaigns get counted.
		FROM (
			SELECT * FROM campaigns c
			WHERE ($1::text[] IS NULL OR c.status = ANY($1))
			  AND ($2 = '' OR c.name ILIKE '%' || $2 || '%' ESCAPE '\' OR c.brief ILIKE '%' || $2 || '%' ESCAPE '\')
			  AND ($4 = '' OR $4 = ANY(c.channels))
			  AND ($5 = 0 OR c.created_at >= now() - make_interval(days => $5))
			ORDER BY `+order+`
			LIMIT $3
		) c
		LEFT JOIN LATERAL (
			SELECT sum(n)::bigint AS total,
			       (sum(n) FILTER (WHERE status IN ('SENT', 'FAILED', 'SKIPPED', 'UNKNOWN')))::bigint AS processed
			FROM (
				SELECT channel, status, count(*) AS n
				FROM campaign_deliveries WHERE campaign_id = c.id
				GROUP BY channel, status
			) counts
		) d ON true
		ORDER BY `+order, statuses, escapeLike(strings.TrimSpace(filter.Query)), limit, filter.Channel, filter.CreatedWithinDays)
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	summaries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Summary, error) {
		var summary Summary
		err := row.Scan(&summary.ID, &summary.Name, &summary.Brief, &summary.Status, &summary.Channels,
			&summary.Total, &summary.Processed, &summary.CreatedAt, &summary.UpdatedAt)
		return summary, err
	})
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	return summaries, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(query string) string {
	return likeEscaper.Replace(query)
}

// Counts are the workspace totals. Total can exceed the sum of the groups, since
// cancelled campaigns belong to none of them.
type Counts struct {
	Total     int `json:"total"`
	Drafts    int `json:"drafts"`
	Active    int `json:"active"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// Overview is the workspace landing data.
// mockProviderName is who both channels send through.
const mockProviderName = "Mock provider"

type Overview struct {
	Counts   Counts          `json:"counts"`
	Recent   []Summary       `json:"recent_campaigns"`
	Activity []ActivityEntry `json:"recent_activity"`
	Channels []ChannelUse    `json:"channels"`
}

// ChannelUse is what provider calls show about a channel. Being able to reach the
// provider is another question, answered by the system check.
type ChannelUse struct {
	Channel        string     `json:"channel"`
	Provider       string     `json:"provider"`
	LastAcceptedAt *time.Time `json:"last_accepted_at"`
	Calls          int        `json:"calls_last_hour"`
	NotAccepted    int        `json:"not_accepted_last_hour"`
}

// Overview summarizes the workspace.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	var overview Overview
	err := s.db.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status = ANY($1)),
		       count(*) FILTER (WHERE status = ANY($2)),
		       count(*) FILTER (WHERE status = ANY($3)),
		       count(*) FILTER (WHERE status = ANY($4))
		FROM campaigns`,
		groupStatuses["drafts"], groupStatuses["active"], groupStatuses["completed"], groupStatuses["failed"],
	).Scan(&overview.Counts.Total, &overview.Counts.Drafts, &overview.Counts.Active, &overview.Counts.Completed, &overview.Counts.Failed)
	if err != nil {
		return Overview{}, fmt.Errorf("count campaigns: %w", err)
	}
	if overview.Recent, err = s.List(ctx, ListFilter{Limit: recentCampaigns}); err != nil {
		return Overview{}, err
	}
	if overview.Activity, err = s.queryActivity(ctx, nil, recentActivity); err != nil {
		return Overview{}, err
	}
	if overview.Channels, err = s.channelUse(ctx); err != nil {
		return Overview{}, err
	}
	return overview, nil
}

// ActivityEntry is one audit event, for people to read.
type ActivityEntry struct {
	CampaignID string          `json:"campaign_id"`
	Name       string          `json:"campaign_name"`
	Type       string          `json:"type"`
	ActorType  string          `json:"actor_type"`
	Actor      *string         `json:"actor"`
	Details    json.RawMessage `json:"details"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// Activity returns a campaign's audit trail, oldest first.
func (s *Service) Activity(ctx context.Context, id string) ([]ActivityEntry, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return nil, ErrNotFound
	}
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	return s.queryActivity(ctx, &parsed, 0)
}

// queryActivity reads one campaign oldest first, or the whole workspace newest
// first when id is nil.
func (s *Service) queryActivity(ctx context.Context, id *pgtype.UUID, limit int) ([]ActivityEntry, error) {
	query := `
		SELECT a.campaign_id::text, c.name, a.event_type, a.actor_type, a.actor, a.details, a.occurred_at
		FROM audit_events a JOIN campaigns c ON c.id = a.campaign_id `
	var args []any
	if id != nil {
		query += `WHERE a.campaign_id = $1 ORDER BY a.id`
		args = append(args, *id)
	} else {
		query += `ORDER BY a.id DESC LIMIT $1`
		args = append(args, limit)
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read activity: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ActivityEntry, error) {
		var entry ActivityEntry
		err := row.Scan(&entry.CampaignID, &entry.Name, &entry.Type, &entry.ActorType, &entry.Actor, &entry.Details, &entry.OccurredAt)
		return entry, err
	})
	if err != nil {
		return nil, fmt.Errorf("read activity: %w", err)
	}
	return entries, nil
}

func (s *Service) channelUse(ctx context.Context) ([]ChannelUse, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ch.channel, $2::text,
		       (SELECT a.occurred_at FROM delivery_attempts a JOIN campaign_deliveries d ON d.id = a.delivery_id
		        WHERE d.channel = ch.channel AND a.outcome = 'accepted' ORDER BY a.occurred_at DESC LIMIT 1),
		       recent.calls, recent.not_accepted
		FROM unnest($1::text[]) AS ch (channel)
		CROSS JOIN LATERAL (
			SELECT count(*) AS calls, count(*) FILTER (WHERE a.outcome <> 'accepted') AS not_accepted
			FROM delivery_attempts a JOIN campaign_deliveries d ON d.id = a.delivery_id
			WHERE d.channel = ch.channel AND a.occurred_at >= $3
		) recent`, channels, mockProviderName, s.now().Add(-time.Hour))
	if err != nil {
		return nil, fmt.Errorf("channel use: %w", err)
	}
	use, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ChannelUse])
	if err != nil {
		return nil, fmt.Errorf("channel use: %w", err)
	}
	return use, nil
}
