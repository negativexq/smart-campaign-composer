package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/schedule"
)

const (
	// DefaultListLimit caps the campaign list.
	DefaultListLimit  = 100
	notificationLimit = 30

	recentCampaigns = 5
	recentActivity  = 8
)

// groupStatuses defines the list filters. Each status belongs to at most one
// group, so overview counts add up; cancelled campaigns only appear under all.
var groupStatuses = map[string][]string{
	"drafts":    {"DRAFT", "GENERATING", "READY_FOR_REVIEW"},
	"active":    {"APPROVED", "SCHEDULED", "DISPATCHING", "PAUSED"},
	"completed": {"COMPLETED"},
	"failed":    {"GENERATION_FAILED", "COMPLETED_WITH_FAILURES"},
}

// listGroups says which campaigns each list filter keeps. The groups above keep their
// statuses; the rest narrow them, so they can overlap: a repeating campaign that is
// sending is in both repeating and sending.
var listGroups = func() map[string]string {
	groups := map[string]string{
		"review":  `c.status = 'READY_FOR_REVIEW'`,
		"sending": `c.status = 'DISPATCHING'`,
		"paused":  `c.status = 'PAUSED'`,
		// Repeats from now on: a schedule with another time to come that is not a single send.
		"repeating": repeating,
	}
	for group, statuses := range groupStatuses {
		groups[group] = `c.status IN ('` + strings.Join(statuses, `', '`) + `')`
	}
	return groups
}()

// repeating keeps campaigns that repeat from now on: a schedule with another time to
// come that is not a single send.
const repeating = `c.status IN ('SCHEDULED', 'DISPATCHING', 'PAUSED') AND EXISTS (
	SELECT 1 FROM campaign_schedules s
	WHERE s.campaign_id = c.id AND s.next_run_at IS NOT NULL AND s.spec->>'kind' <> 'once')`

// listGroupOrder is the order group counts are read in.
var listGroupOrder = []string{"drafts", "review", "active", "repeating", "sending", "paused", "completed", "failed"}

// archivedGroup holds the archived campaigns, which every other group leaves out.
const archivedGroup = "archived"

// ErrUnknownGroup means a list filter does not exist.
var ErrUnknownGroup = errors.New("unknown campaign group")

// ListFilter narrows the campaign list. An empty Group means all campaigns.
type ListFilter struct {
	Group             string
	Query             string
	Channel           string
	CreatedWithinDays int
	// Tag keeps campaigns carrying this tag, spelled as it is stored.
	Tag    string
	Sort   string
	Limit  int
	Offset int
}

// listOrders holds the only orderings a list can ask for, so no request text
// reaches the query.
var listOrders = func() map[string]string {
	// sort_next and sort_last come from sortJoins, read once for the whole list.
	lastStarted := `sort_last DESC NULLS LAST`
	// What sends soonest first, then what sent last, then the newest.
	next := `sort_next NULLS LAST, ` + lastStarted + `, c.created_at DESC, c.id`
	return map[string]string{
		"":          next,
		"next":      next,
		"last_sent": lastStarted + `, c.created_at DESC, c.id`,
		"newest":    "c.created_at DESC, c.id",
		"oldest":    "c.created_at, c.id",
		"name":      "lower(c.name), c.id",
	}
}()

// sortJoins brings each campaign's next send and latest run start for ordering by
// them. Joined once, they cost one pass over schedules and runs; looked up per
// campaign, they cost two lookups for every campaign that matches, not only the page.
const sortJoins = `
	LEFT JOIN campaign_schedules sort_sch ON sort_sch.campaign_id = c.id
	LEFT JOIN (SELECT campaign_id, max(started_at) AS started_at FROM campaign_runs GROUP BY campaign_id) sort_run ON sort_run.campaign_id = c.id`

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
	// Schedule is the sending rule, when the campaign has one.
	Schedule *ListSchedule `json:"schedule"`
	Tags     []string      `json:"tags"`
	// ArchivedAt is when the campaign left the list, if it did.
	ArchivedAt *time.Time `json:"archived_at"`
	// LastRun is the latest send, so a repeating campaign shows its last result rather
	// than every run added up.
	LastRun *RunSummary `json:"last_run"`
}

// ListSchedule is what a list row says about when a campaign sends.
type ListSchedule struct {
	Description string     `json:"description"`
	Repeats     bool       `json:"repeats"`
	NextRunAt   *time.Time `json:"next_run_at"`
	Stopped     bool       `json:"stopped"`
}

// RunSummary is one send of a campaign and how its messages ended.
type RunSummary struct {
	Number     int        `json:"number"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Total      int        `json:"total"`
	Processed  int        `json:"processed"`
	Sent       int        `json:"sent"`
	Failed     int        `json:"failed"`
	Skipped    int        `json:"skipped"`
	Unknown    int        `json:"unknown"`
}

// runCounts adds up run r's deliveries by how they ended: from the counts a finished
// run kept, or by counting a run still sending.
const runCounts = `
	SELECT COALESCE(sum(n), 0)::int AS total,
	       COALESCE(sum(n) FILTER (WHERE st IN ('SENT', 'FAILED', 'SKIPPED', 'UNKNOWN')), 0)::int AS processed,
	       COALESCE(sum(n) FILTER (WHERE st = 'SENT'), 0)::int AS sent,
	       COALESCE(sum(n) FILTER (WHERE st = 'FAILED'), 0)::int AS failed,
	       COALESCE(sum(n) FILTER (WHERE st = 'SKIPPED'), 0)::int AS skipped,
	       COALESCE(sum(n) FILTER (WHERE st = 'UNKNOWN'), 0)::int AS unknown
	FROM (
		SELECT e->>1 AS st, (e->>2)::int AS n FROM jsonb_array_elements(r.counts) e WHERE r.finished_at IS NOT NULL
		UNION ALL
		SELECT d.status, count(*)::int FROM campaign_deliveries d WHERE d.run_id = r.id AND r.finished_at IS NULL GROUP BY d.status
	) counted`

// matching gives the FROM and WHERE of the campaigns a filter keeps, shared by the
// list and its total, with their arguments. withGroup false leaves the group and the
// archive out, for counting every group at once.
func (f ListFilter) matching(withGroup bool) (string, []any, error) {
	group := "true"
	if withGroup {
		switch known, ok := listGroups[f.Group]; {
		case f.Group == archivedGroup:
			group = "c.archived_at IS NOT NULL"
		case f.Group == "":
			group = "c.archived_at IS NULL"
		case ok:
			group = "c.archived_at IS NULL AND " + known
		default:
			return "", nil, ErrUnknownGroup
		}
	}
	if _, known := listOrders[f.Sort]; !known || f.CreatedWithinDays < 0 || (f.Channel != "" && f.Channel != "sms" && f.Channel != "email") {
		return "", nil, ErrUnknownFilter
	}
	// Only the filters in use become conditions. A condition such as "$1 = '' OR ..."
	// hides how many rows match from a reused plan, which then picks joins for a
	// handful of campaigns and runs them for thousands.
	conditions := []string{"c.deleted_at IS NULL", "(" + group + ")"}
	args := []any{}
	param := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if query := strings.TrimSpace(f.Query); query != "" {
		like := param("%" + escapeLike(query) + "%")
		conditions = append(conditions, `(c.name ILIKE `+like+` ESCAPE '\' OR c.brief ILIKE `+like+` ESCAPE '\'
		       OR EXISTS (SELECT 1 FROM unnest(c.tags) t WHERE t ILIKE `+like+` ESCAPE '\'))`)
	}
	if f.Channel != "" {
		conditions = append(conditions, param(f.Channel)+" = ANY(c.channels)")
	}
	if f.CreatedWithinDays > 0 {
		conditions = append(conditions, "c.created_at >= now() - make_interval(days => "+param(f.CreatedWithinDays)+")")
	}
	if f.Tag != "" {
		conditions = append(conditions, param(f.Tag)+" = ANY(c.tags)")
	}
	return `
	FROM campaigns c
	WHERE ` + strings.Join(conditions, "\n\t  AND "), args, nil
}

// CountGroups says how many campaigns each list group holds with the filter's search
// and channel, whatever group the filter itself picks. "all" holds every one.
func (s *Service) CountGroups(ctx context.Context, filter ListFilter) (map[string]int, error) {
	matching, args, err := filter.matching(false)
	if err != nil {
		return nil, err
	}
	// Campaigns are first counted by status, archive and whether they repeat, so each
	// group's condition is checked on a few counted rows rather than on every campaign.
	columns := "COALESCE(sum(n) FILTER (WHERE NOT c.archived), 0)::int"
	for _, group := range listGroupOrder {
		condition := listGroups[group]
		if group == "repeating" {
			condition = "c.repeats"
		}
		columns += ", COALESCE(sum(n) FILTER (WHERE NOT c.archived AND " + condition + "), 0)::int"
	}
	columns += ", COALESCE(sum(n) FILTER (WHERE c.archived), 0)::int"
	counts := make([]int, len(listGroupOrder)+2)
	targets := make([]any, len(counts))
	for i := range counts {
		targets[i] = &counts[i]
	}
	counted := `(SELECT c.status, c.archived_at IS NOT NULL AS archived, ` + repeating + ` AS repeats, count(*) AS n` +
		matching + ` GROUP BY 1, 2, 3) c`
	if err := s.db.QueryRow(ctx, `SELECT `+columns+` FROM `+counted, args...).Scan(targets...); err != nil {
		return nil, fmt.Errorf("count campaign groups: %w", err)
	}
	groups := map[string]int{"all": counts[0], archivedGroup: counts[len(counts)-1]}
	for i, group := range listGroupOrder {
		groups[group] = counts[i+1]
	}
	return groups, nil
}

// Upcoming is a send planned for later.
type Upcoming struct {
	CampaignID string    `json:"campaign_id"`
	Name       string    `json:"name"`
	At         time.Time `json:"at"`
	Repeats    bool      `json:"repeats"`
}

// UpcomingSends lists the next sends across the workspace, soonest first.
func (s *Service) UpcomingSends(ctx context.Context, limit int) ([]Upcoming, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id::text, c.name, s.next_run_at, s.spec->>'kind' <> 'once'
		FROM campaign_schedules s JOIN campaigns c ON c.id = s.campaign_id
		WHERE s.next_run_at IS NOT NULL AND c.status IN ('SCHEDULED', 'DISPATCHING') AND c.archived_at IS NULL AND c.deleted_at IS NULL
		ORDER BY s.next_run_at, c.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("upcoming sends: %w", err)
	}
	upcoming, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Upcoming])
	if err != nil {
		return nil, fmt.Errorf("upcoming sends: %w", err)
	}
	return upcoming, nil
}

// List returns one page of campaigns, by default what sends next first.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Summary, error) {
	matching, args, err := filter.matching(true)
	if err != nil {
		return nil, err
	}
	order := listOrders[filter.Sort]
	limit := filter.Limit
	if limit <= 0 || limit > DefaultListLimit {
		limit = DefaultListLimit
	}
	// Sort keys from other tables are joined only when the order uses them.
	page := `SELECT c.*, NULL::timestamptz AS sort_next, NULL::timestamptz AS sort_last` + matching
	if strings.Contains(order, "sort_") {
		page = `SELECT c.*, sort_sch.next_run_at AS sort_next, sort_run.started_at AS sort_last` +
			strings.Replace(matching, "FROM campaigns c", "FROM campaigns c"+sortJoins, 1)
	}

	rows, err := s.db.Query(ctx, `
		SELECT c.id::text, c.name, c.brief, c.status,
		       c.channels, COALESCE(d.total, 0), COALESCE(d.processed, 0),
		       c.created_at, c.updated_at, c.tags, c.archived_at, sch.spec, sch.next_run_at, sch.last_run_at,
		       last.number, last.started_at, last.finished_at, last.total, last.processed, last.sent, last.failed, last.skipped, last.unknown
		-- Choosing the page first means only its campaigns get counted.
		FROM (`+page+`
			ORDER BY `+order+`
			LIMIT `+fmt.Sprintf("$%d OFFSET $%d", len(args)+1, len(args)+2)+`
		) c
		LEFT JOIN campaign_schedules sch ON sch.campaign_id = c.id
		LEFT JOIN LATERAL (
			SELECT r.number, r.started_at, r.finished_at, counted.*
			FROM campaign_runs r CROSS JOIN LATERAL (`+runCounts+`) counted
			WHERE r.campaign_id = c.id
			ORDER BY r.number DESC LIMIT 1
		) last ON true
		-- Finished runs kept their counts; only a run still sending is counted here.
		LEFT JOIN LATERAL (
			SELECT COALESCE(sum(r.total), 0) + COALESCE(sum(live.total), 0) AS total,
			       COALESCE(sum(r.total), 0) + COALESCE(sum(live.processed), 0) AS processed
			FROM campaign_runs r
			LEFT JOIN LATERAL (
				SELECT count(*) AS total,
				       count(*) FILTER (WHERE status IN ('SENT', 'FAILED', 'SKIPPED', 'UNKNOWN')) AS processed
				FROM campaign_deliveries WHERE run_id = r.id AND r.finished_at IS NULL
			) live ON true
			WHERE r.campaign_id = c.id
		) d ON true
		ORDER BY `+order, append(args, limit, max(filter.Offset, 0))...)
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	summaries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Summary, error) {
		var (
			summary  Summary
			spec     []byte
			next     *time.Time
			lastRun  *time.Time
			number   *int
			started  *time.Time
			finished *time.Time
			counts   [6]*int
		)
		err := row.Scan(&summary.ID, &summary.Name, &summary.Brief, &summary.Status, &summary.Channels,
			&summary.Total, &summary.Processed, &summary.CreatedAt, &summary.UpdatedAt, &summary.Tags, &summary.ArchivedAt, &spec, &next, &lastRun,
			&number, &started, &finished, &counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5])
		if err != nil {
			return summary, err
		}
		if spec != nil {
			var rule schedule.Spec
			if err := json.Unmarshal(spec, &rule); err != nil {
				return summary, fmt.Errorf("decode schedule: %w", err)
			}
			summary.Schedule = &ListSchedule{Description: rule.Describe(), Repeats: rule.Kind != schedule.KindOnce, NextRunAt: next, Stopped: stopped(rule, next, lastRun)}
		}
		if number != nil {
			summary.LastRun = &RunSummary{Number: *number, StartedAt: *started, FinishedAt: finished,
				Total: *counts[0], Processed: *counts[1], Sent: *counts[2], Failed: *counts[3], Skipped: *counts[4], Unknown: *counts[5]}
		}
		return summary, nil
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
	// BusiestSecond is the most calls made in one second, to compare with the rate limit.
	BusiestSecond int `json:"busiest_second_last_hour"`
}

// Channels says what provider calls show about each channel.
func (s *Service) Channels(ctx context.Context) ([]ChannelUse, error) {
	return s.channelUse(ctx)
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
		FROM campaigns WHERE deleted_at IS NULL AND archived_at IS NULL`,
		groupStatuses["drafts"], groupStatuses["active"], groupStatuses["completed"], groupStatuses["failed"],
	).Scan(&overview.Counts.Total, &overview.Counts.Drafts, &overview.Counts.Active, &overview.Counts.Completed, &overview.Counts.Failed)
	if err != nil {
		return Overview{}, fmt.Errorf("count campaigns: %w", err)
	}
	if overview.Recent, err = s.List(ctx, ListFilter{Sort: "newest", Limit: recentCampaigns}); err != nil {
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
	ID         int64           `json:"id"`
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
		SELECT a.id, a.campaign_id::text, c.name, a.event_type, a.actor_type, a.actor, a.details, a.occurred_at
		FROM audit_events a JOIN campaigns c ON c.id = a.campaign_id `
	var args []any
	if id != nil {
		query += `WHERE a.campaign_id = $1 ORDER BY a.id`
		args = append(args, *id)
	} else {
		query += `WHERE c.deleted_at IS NULL ORDER BY a.id DESC LIMIT $1`
		args = append(args, limit)
	}
	return s.readActivity(ctx, query, args...)
}

// EmailTemplateUse is one design and how many campaigns currently use it.
type EmailTemplateUse struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Campaigns int    `json:"campaigns"`
}

// EmailTemplateUses lists every design with its use. A campaign counts once, by its
// latest version or, with no message yet, by the design it was started from. One with
// no choice uses the default, as it would when sent.
func (s *Service) EmailTemplateUses(ctx context.Context) ([]EmailTemplateUse, error) {
	counts, err := s.templateCounts(ctx)
	if err != nil {
		return nil, err
	}
	uses := make([]EmailTemplateUse, 0, len(content.EmailTemplates))
	for _, id := range content.EmailTemplates {
		uses = append(uses, EmailTemplateUse{ID: id, Version: content.EmailTemplateVersion(id), Campaigns: counts[id]})
	}
	return uses, nil
}

func (s *Service) templateCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(NULLIF(COALESCE(v.content->>'email_template', c.email_template), ''), $1), count(*)
		FROM campaigns c
		LEFT JOIN LATERAL (
			SELECT content FROM campaign_versions WHERE campaign_id = c.id ORDER BY version DESC LIMIT 1
		) v ON true
		WHERE 'email' = ANY(c.channels) AND c.deleted_at IS NULL
		GROUP BY 1`, content.EmailTemplates[0])
	if err != nil {
		return nil, fmt.Errorf("count template use: %w", err)
	}
	counts := map[string]int{}
	var (
		name  string
		count int
	)
	if _, err := pgx.ForEachRow(rows, []any{&name, &count}, func() error {
		counts[name] = count
		return nil
	}); err != nil {
		return nil, fmt.Errorf("count template use: %w", err)
	}
	return counts, nil
}

// Notifications are the events someone would want to hear about without watching:
// a send that started on its own, one that ended, and anything that went wrong,
// including a channel alert while it is open and the news that it cleared.
// A scheduled run with nobody new to reach said nothing happened, so it is left out,
// and a problem since put right, by resuming or by writing again, is no longer news.
func (s *Service) Notifications(ctx context.Context) ([]ActivityEntry, error) {
	return s.readActivity(ctx, `
		SELECT a.id, a.campaign_id::text, c.name, a.event_type, a.actor_type, a.actor, a.details, a.occurred_at
		FROM audit_events a JOIN campaigns c ON c.id = a.campaign_id
		WHERE c.deleted_at IS NULL
		  AND (a.event_type IN ('campaign.completed', 'campaign.run_finished')
		   OR (a.event_type = 'campaign.paused' AND c.status = 'PAUSED')
		   OR (a.event_type = 'content.generation_failed' AND c.status = 'GENERATION_FAILED')
		   OR (a.event_type = 'campaign.run' AND (a.details->>'queued')::int > 0)
		   OR a.event_type = 'campaign.alert_closed'
		   OR (a.event_type = 'campaign.alert_opened' AND NOT EXISTS (
				SELECT 1 FROM audit_events later
				WHERE later.campaign_id = a.campaign_id AND later.id > a.id
				  AND later.event_type IN ('campaign.alert_opened', 'campaign.alert_closed')
				  AND later.details->>'channel' = a.details->>'channel' AND later.details->>'alert' = a.details->>'alert')))
		ORDER BY a.id DESC LIMIT $1`, notificationLimit)
}

func (s *Service) readActivity(ctx context.Context, query string, args ...any) ([]ActivityEntry, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read activity: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ActivityEntry, error) {
		var entry ActivityEntry
		err := row.Scan(&entry.ID, &entry.CampaignID, &entry.Name, &entry.Type, &entry.ActorType, &entry.Actor, &entry.Details, &entry.OccurredAt)
		return entry, err
	})
	if err != nil {
		return nil, fmt.Errorf("read activity: %w", err)
	}
	return entries, nil
}

// channelUseFresh is how long a reading of channel use is reused. Counting an hour of
// calls costs in proportion to the calls, so under read load every overview counting
// again made it the most expensive query; a few seconds' lag on an hour's totals is not
// something anyone can see.
const channelUseFresh = 5 * time.Second

// channelUseCache keeps the last reading. Callers that arrive while it is being read
// wait for that reading instead of starting their own.
type channelUseCache struct {
	mu     sync.Mutex
	use    []ChannelUse
	readAt time.Time
}

func (s *Service) channelUse(ctx context.Context) ([]ChannelUse, error) {
	cache := &s.channels
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.use != nil && time.Since(cache.readAt) < channelUseFresh {
		return slices.Clone(cache.use), nil
	}
	// One pass over the hour: calls per second, then their sum and the busiest.
	rows, err := s.db.Query(ctx, `
		SELECT ch.channel, $2::text,
		       (SELECT a.occurred_at FROM delivery_attempts a
		        WHERE a.channel = ch.channel AND a.outcome = 'accepted' ORDER BY a.occurred_at DESC LIMIT 1),
		       COALESCE(sum(second.calls), 0)::int, COALESCE(sum(second.not_accepted), 0)::int, COALESCE(max(second.calls), 0)::int
		FROM unnest($1::text[]) AS ch (channel)
		LEFT JOIN LATERAL (
			SELECT count(*) AS calls, count(*) FILTER (WHERE a.outcome <> 'accepted') AS not_accepted
			FROM delivery_attempts a
			WHERE a.channel = ch.channel AND a.occurred_at >= $3
			GROUP BY date_trunc('second', a.occurred_at)
		) second ON true
		GROUP BY ch.channel
		ORDER BY array_position($1::text[], ch.channel)`, channels, mockProviderName, s.now().Add(-time.Hour))
	if err != nil {
		return nil, fmt.Errorf("channel use: %w", err)
	}
	use, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ChannelUse])
	if err != nil {
		return nil, fmt.Errorf("channel use: %w", err)
	}
	cache.use, cache.readAt = use, time.Now()
	return slices.Clone(use), nil
}

// CustomerCount is how many customers exist, whether or not they can be messaged.
func (s *Service) CustomerCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count customers: %w", err)
	}
	return count, nil
}
