package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/negativexq/smart-campaign-composer/internal/schedule"
)

// The longest range a calendar asks for: a month grid is six weeks.
const maxCalendarRange = 43 * 24 * time.Hour

// Limits that keep one calendar answer small. An hourly campaign fills a month with
// hundreds of sends, so each campaign brings at most calendarSendsPerCampaign of them.
const (
	calendarCampaigns        = 300
	calendarSendsPerCampaign = 200
	maxOccurrenceSteps       = 20000
)

// ErrCalendarRange means a calendar range is missing, reversed or too long.
var ErrCalendarRange = errors.New("calendar range must be a start before an end, at most 43 days apart")

// What a calendar entry stands for.
const (
	// EntryDraft is a campaign not yet sent or scheduled, on the day it last changed.
	EntryDraft = "draft"
	// EntryScheduled is a send still to come.
	EntryScheduled = "scheduled"
	// EntrySending is a run that has started and not finished.
	EntrySending = "sending"
	// EntrySent is a run that finished.
	EntrySent = "sent"
)

// CalendarEntry is one thing on the calendar: a run, a send to come, or a draft.
type CalendarEntry struct {
	CampaignID string    `json:"campaign_id"`
	Kind       string    `json:"kind"`
	At         time.Time `json:"at"`
	// Run is the run an entry of kind sent or sending stands for.
	Run *RunSummary `json:"run,omitempty"`
}

// Calendar is what a calendar range shows, with the campaigns its entries belong to.
type Calendar struct {
	Entries   []CalendarEntry `json:"entries"`
	Campaigns []Summary       `json:"campaigns"`
	// Truncated says some sends were left out to keep the answer small.
	Truncated bool `json:"truncated"`
}

// draftStatuses are the statuses a campaign shows as a draft in: not sent, not
// scheduled, and not cancelled.
var draftStatuses = []string{"DRAFT", "GENERATING", "READY_FOR_REVIEW", "GENERATION_FAILED", "APPROVED"}

// scheduledStatuses are the statuses whose next send is still going to happen, or
// will once the campaign is resumed.
var scheduledStatuses = []string{"SCHEDULED", "DISPATCHING", "PAUSED"}

// Calendar returns what happens to the campaigns a filter keeps between from and to:
// the runs that started, the sends still to come, and drafts on the day they last
// changed. It is the list's data placed in time, so the filter is the list's.
func (s *Service) Calendar(ctx context.Context, filter ListFilter, from, to time.Time) (Calendar, error) {
	if !from.Before(to) || to.Sub(from) > maxCalendarRange {
		return Calendar{}, ErrCalendarRange
	}
	matching, args, err := filter.matching(true)
	if err != nil {
		return Calendar{}, err
	}
	param := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	fromParam, toParam := param(from), param(to)
	drafts, scheduled := param(draftStatuses), param(scheduledStatuses)
	rows, err := s.db.Query(ctx, `SELECT c.id::text`+matching+`
		  AND (EXISTS (SELECT 1 FROM campaign_runs r WHERE r.campaign_id = c.id AND r.started_at < `+toParam+`
		               AND (r.finished_at IS NULL OR r.started_at >= `+fromParam+`))
		       OR (c.status = ANY(`+scheduled+`) AND EXISTS (SELECT 1 FROM campaign_schedules s
		               WHERE s.campaign_id = c.id AND s.next_run_at < `+toParam+`))
		       OR (c.status = ANY(`+drafts+`) AND NOT EXISTS (SELECT 1 FROM campaign_runs r WHERE r.campaign_id = c.id)
		           AND c.updated_at >= `+fromParam+` AND c.updated_at < `+toParam+`))
		ORDER BY c.id
		LIMIT `+param(calendarCampaigns+1), args...)
	if err != nil {
		return Calendar{}, fmt.Errorf("calendar campaigns: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return Calendar{}, fmt.Errorf("calendar campaigns: %w", err)
	}
	calendar := Calendar{Entries: []CalendarEntry{}, Campaigns: []Summary{}}
	if len(ids) > calendarCampaigns {
		ids, calendar.Truncated = ids[:calendarCampaigns], true
	}
	if len(ids) == 0 {
		return calendar, nil
	}
	if calendar.Campaigns, err = s.summaries(ctx,
		`SELECT c.*, NULL::timestamptz AS sort_next, NULL::timestamptz AS sort_last FROM campaigns c WHERE c.id = ANY($1::uuid[])`,
		"c.name, c.id", []any{ids}, ""); err != nil {
		return Calendar{}, err
	}

	runs, err := s.calendarRuns(ctx, ids, from, to)
	if err != nil {
		return Calendar{}, err
	}
	calendar.Entries = append(calendar.Entries, runs...)
	sends, truncated, err := s.calendarSends(ctx, ids, from, to)
	if err != nil {
		return Calendar{}, err
	}
	calendar.Entries = append(calendar.Entries, sends...)
	calendar.Truncated = calendar.Truncated || truncated
	for _, campaign := range calendar.Campaigns {
		if campaign.LastRun == nil && campaign.Schedule == nil && slices.Contains(draftStatuses, campaign.Status) &&
			!campaign.UpdatedAt.Before(from) && campaign.UpdatedAt.Before(to) {
			calendar.Entries = append(calendar.Entries, CalendarEntry{CampaignID: campaign.ID, Kind: EntryDraft, At: campaign.UpdatedAt})
		}
	}
	sort.SliceStable(calendar.Entries, func(i, j int) bool { return calendar.Entries[i].At.Before(calendar.Entries[j].At) })
	return calendar, nil
}

// calendarRuns gives the runs of the campaigns that started in the range, and the
// runs still sending that started before it.
func (s *Service) calendarRuns(ctx context.Context, ids []string, from, to time.Time) ([]CalendarEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.campaign_id::text, r.number, r.started_at, r.finished_at,
		       counted.total, counted.processed, counted.sent, counted.failed, counted.skipped, counted.unknown
		FROM campaign_runs r CROSS JOIN LATERAL (`+runCounts+`) counted
		WHERE r.campaign_id = ANY($1::uuid[]) AND r.started_at < $3 AND (r.finished_at IS NULL OR r.started_at >= $2)
		ORDER BY r.started_at`, ids, from, to)
	if err != nil {
		return nil, fmt.Errorf("calendar runs: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CalendarEntry, error) {
		var entry CalendarEntry
		run := &RunSummary{}
		if err := row.Scan(&entry.CampaignID, &run.Number, &run.StartedAt, &run.FinishedAt,
			&run.Total, &run.Processed, &run.Sent, &run.Failed, &run.Skipped, &run.Unknown); err != nil {
			return entry, err
		}
		entry.Kind, entry.At, entry.Run = EntrySent, run.StartedAt, run
		if run.FinishedAt == nil {
			entry.Kind = EntrySending
			// A run still sending that began before the range shows where it is now.
			if entry.At.Before(from) {
				entry.At = from
			}
		}
		return entry, nil
	})
	if err != nil {
		return nil, fmt.Errorf("calendar runs: %w", err)
	}
	return entries, nil
}

// calendarSends gives the sends still to come in the range, from each schedule's
// next time and its rule. It says whether a campaign had more than the limit.
func (s *Service) calendarSends(ctx context.Context, ids []string, from, to time.Time) ([]CalendarEntry, bool, error) {
	rows, err := s.db.Query(ctx, `
		SELECT s.campaign_id::text, s.spec, s.next_run_at
		FROM campaign_schedules s JOIN campaigns c ON c.id = s.campaign_id
		WHERE s.campaign_id = ANY($1::uuid[]) AND c.status = ANY($2) AND s.next_run_at < $3`, ids, scheduledStatuses, to)
	if err != nil {
		return nil, false, fmt.Errorf("calendar sends: %w", err)
	}
	type planned struct {
		id   string
		spec []byte
		next time.Time
	}
	schedules, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (planned, error) {
		var p planned
		return p, row.Scan(&p.id, &p.spec, &p.next)
	})
	if err != nil {
		return nil, false, fmt.Errorf("calendar sends: %w", err)
	}
	entries := []CalendarEntry{}
	truncated := false
	for _, p := range schedules {
		var rule schedule.Spec
		if err := json.Unmarshal(p.spec, &rule); err != nil {
			return nil, false, fmt.Errorf("decode schedule: %w", err)
		}
		sends, more := Occurrences(rule, p.next, from, to, calendarSendsPerCampaign)
		for _, at := range sends {
			entries = append(entries, CalendarEntry{CampaignID: p.id, Kind: EntryScheduled, At: at})
		}
		truncated = truncated || more
	}
	return entries, truncated, nil
}

// Occurrences lists a rule's sends from next, the one already planned, that fall in
// [from, to), at most limit of them. It says whether more were left out.
func Occurrences(rule schedule.Spec, next, from, to time.Time, limit int) ([]time.Time, bool) {
	sends := []time.Time{}
	at, ok := next, true
	// A range far ahead of an hourly rule is reached in a bounded number of steps.
	for steps := 0; ok && at.Before(to) && steps < maxOccurrenceSteps; steps++ {
		if !at.Before(from) {
			if len(sends) == limit {
				return sends, true
			}
			sends = append(sends, at)
		}
		at, ok = rule.Next(at)
	}
	// Still short of the range's end means the step limit stopped it.
	return sends, ok && at.Before(to)
}
