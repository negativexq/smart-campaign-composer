package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/schedule"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

const (
	statusApproved    = "APPROVED"
	statusDispatching = "DISPATCHING"
	statusPaused      = "PAUSED"

	statusCompleted             = "COMPLETED"
	statusCompletedWithFailures = "COMPLETED_WITH_FAILURES"

	eventApproved = "campaign.approved"

	eventDispatched      = "campaign.dispatched"
	eventResumed         = "campaign.resumed"
	reasonWithoutConsent = "no_consent"

	eventExported        = "deliveries.exported"
	maxCallsFilter       = 1000
	defaultDeliveryLimit = 200
	maxDeliveryLimit     = 500
)

// Errors the API maps to responses.
var (
	ErrNotReviewable = errors.New("campaign has no messages ready for review")
	ErrNotApproved   = errors.New("campaign has not been approved")
	ErrUnknownFilter = errors.New("unknown delivery filter")
	ErrUnknownEvent  = errors.New("unknown delivery event")
	ErrNotPaused     = errors.New("campaign is not paused")
)

var (
	deliveryStatuses = []string{"QUEUED", "SENDING", "SENT", "RETRYING", "FAILED", "SKIPPED", "UNKNOWN"}
	channels         = []string{"sms", "email"}
	attemptOutcomes  = []string{"accepted", "rate_limited", "server_error", "rejected", "timeout", "unauthorized"}
)

// lockStatus takes the campaign's row lock, which edits and generation also need,
// so what is checked under it cannot change before the transaction ends.
func lockStatus(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return "", ErrNotFound
	}
	var (
		status   string
		archived bool
	)
	err := tx.QueryRow(ctx, `SELECT status, archived_at IS NOT NULL FROM campaigns WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, parsed).Scan(&status, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock campaign: %w", err)
	}
	// An archived campaign keeps its report but sends nothing until it is restored.
	if archived {
		return "", ErrArchived
	}
	return status, nil
}

// Approve is the human decision that freezes what will be sent. Everything is
// checked again here, because users and consents may have changed during review.
func (s *Service) Approve(ctx context.Context, id string) (View, error) {
	return s.ApproveAt(ctx, id, "")
}

// ApproveAt approves the campaign as it was shown at revision, its ReviewRevision.
// Messages edited or rewritten, or an audience changed, after the review page loaded
// are refused, since the person approving never saw them. An empty revision skips
// that check. Repeating an approval that took effect changes nothing.
func (s *Service) ApproveAt(ctx context.Context, id, revision string) (View, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin approve: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	status, err := lockStatus(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	switch status {
	case statusReady:
	case statusApproved, statusScheduled, statusDispatching, statusPaused, statusCompleted, statusCompletedWithFailures:
		return s.get(ctx, tx, id)
	default:
		return View{}, ErrNotReviewable
	}
	view, err := s.get(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if view.Content == nil {
		return View{}, ErrNotReviewable
	}
	if revision != "" && revision != view.ReviewRevision {
		return View{}, ErrReviewChanged
	}
	if len(view.Blockers) > 0 {
		return View{}, ErrAudienceBlocked
	}
	if err := checkVersions(&view); err != nil {
		return View{}, err
	}

	breakdown, err := segment.Narrow(ctx, tx, view.Proposal.Segment, view.AudienceAsOf)
	if err != nil {
		return View{}, err
	}
	snapshot, err := json.Marshal(AudienceSnapshot{Counts: view.Audience, Breakdown: breakdown, Languages: view.Languages, Variants: view.VariantAudience})
	if err != nil {
		return View{}, fmt.Errorf("encode audience snapshot: %w", err)
	}
	if err := keepRecipients(ctx, tx, id, view.Version, view.Proposal.Segment, view.AudienceAsOf); err != nil {
		return View{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE campaign_versions SET approved_at = now(), audience_snapshot = $3, recipients_kept = true WHERE campaign_id = $1 AND version = $2`, id, view.Version, snapshot); err != nil {
		return View{}, fmt.Errorf("approve version: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, statusApproved); err != nil {
		return View{}, fmt.Errorf("approve campaign: %w", err)
	}
	details := map[string]any{"version": view.Version, "audience": view.Audience, "warnings": len(view.ContentWarnings)}
	// What the design looked like when approved, since designs live in code and can change.
	if view.Content != nil && slices.Contains(view.Channels, "email") {
		details["email_template"] = view.Content.EmailTemplate
		details["email_template_version"] = content.TemplateVersion(view.Content.Content)
		if view.Content.EmailDesign != nil {
			details["email_template_name"] = view.Content.EmailDesign.Name
		}
	}
	if err := recordAudit(ctx, tx, id, eventApproved, details); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit approve: %w", err)
	}
	return s.Get(ctx, id)
}

// keepRecipients stores the people the counted audience is made of, whatever their
// consent: someone who agrees before the send is still one of the people approved.
func keepRecipients(ctx context.Context, tx pgx.Tx, id string, version int, spec segment.Spec, asOf time.Time) error {
	where, args := segment.Compile(spec, asOf)
	campaignParam := fmt.Sprintf("$%d", len(args)+1)
	versionParam := fmt.Sprintf("$%d", len(args)+2)
	args = append(args, id, version)
	if _, err := tx.Exec(ctx, `
		INSERT INTO campaign_recipients (campaign_version_id, user_id)
		SELECT (SELECT v.id FROM campaign_versions v WHERE v.campaign_id = `+campaignParam+`::uuid AND v.version = `+versionParam+`::int), u.id
		FROM users u
		WHERE `+where, args...); err != nil {
		return fmt.Errorf("keep recipients: %w", err)
	}
	return nil
}

// Dispatch queues one delivery per approved person and channel.
// People without consent are recorded as skipped, so the report can account for
// everyone. Only someone skipped, who was therefore never messaged, can be queued
// by a later run. Repeating it changes nothing.
func (s *Service) Dispatch(ctx context.Context, id string) (view View, err error) {
	ctx, span := s.tracer.Start(ctx, "campaign.dispatch", trace.WithAttributes(attribute.String("campaign.id", id)))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "dispatch failed")
		}
		span.End()
	}()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin dispatch: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	status, err := lockStatus(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	switch status {
	case statusApproved:
	case statusDispatching, statusPaused, statusCompleted, statusCompletedWithFailures:
		return s.get(ctx, tx, id)
	case statusScheduled:
		return View{}, ErrAlreadyScheduled
	default:
		return View{}, ErrNotApproved
	}
	stored, err := s.loadFrom(ctx, tx, id)
	if err != nil {
		return View{}, err
	}

	var kept bool
	if err := tx.QueryRow(ctx, `SELECT recipients_kept FROM campaign_versions WHERE campaign_id = $1 AND version = $2`, id, stored.Version).Scan(&kept); err != nil {
		return View{}, fmt.Errorf("read recipients: %w", err)
	}
	asOf := stored.AudienceAsOf
	if kept {
		asOf = s.now()
	}
	queued, skipped, err := queueRun(ctx, tx, id, s.now(), schedule.Spec{}, stored.Version, stored.Channels, testShare(stored.ABTest, stored.VariantAPercent), stored.Proposal.Segment, kept, asOf, traceParent(ctx))
	if err != nil {
		return View{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, statusDispatching); err != nil {
		return View{}, fmt.Errorf("start dispatch: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventDispatched, map[string]any{"queued": queued, "skipped": skipped}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit dispatch: %w", err)
	}
	span.SetAttributes(attribute.Int("delivery.queued", queued), attribute.Int("delivery.skipped", skipped))
	return s.Get(ctx, id)
}

// Resume carries on with a paused campaign. Its messages kept their place and their
// idempotency keys, so nobody is messaged twice. Repeating it changes nothing.
func (s *Service) Resume(ctx context.Context, id string) (View, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin resume: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	status, err := lockStatus(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	switch status {
	case statusPaused:
	case statusDispatching:
		return s.get(ctx, tx, id)
	default:
		return View{}, ErrNotPaused
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, statusDispatching); err != nil {
		return View{}, fmt.Errorf("resume campaign: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventResumed, map[string]any{}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit resume: %w", err)
	}
	return s.Get(ctx, id)
}

func traceParent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

// variantOf is the side of a test someone lands on: always the same one, on every
// channel and run, so what approval counts is what sending does. Each person holds a
// fixed place from 0 to 99, and the share entered for version A is a cut through them.
func variantOf(campaign, percent string) string {
	return `CASE WHEN ('x' || substr(md5(` + campaign + `::uuid::text || ':' || u.id), 1, 8))::bit(32)::bigint % 100 < ` + percent + `::int THEN 'A' ELSE 'B' END`
}

// testShare is what insertDeliveries needs to split people: nothing without a test.
func testShare(abTest bool, percent int) int {
	if abTest {
		return percent
	}
	return 0
}

// openRun starts the next send of a campaign, whose row the caller holds locked.
func openRun(ctx context.Context, tx pgx.Tx, id string, startedAt time.Time) (string, error) {
	var runID string
	err := tx.QueryRow(ctx, `
		INSERT INTO campaign_runs (campaign_id, number, started_at)
		SELECT $1, COALESCE(max(number), 0) + 1, $2 FROM campaign_runs WHERE campaign_id = $1
		RETURNING id::text`, id, startedAt).Scan(&runID)
	if err != nil {
		return "", fmt.Errorf("open run: %w", err)
	}
	return runID, nil
}

// queueRun opens a run and queues its deliveries.
func queueRun(ctx context.Context, tx pgx.Tx, id string, startedAt time.Time, rule schedule.Spec, version int, channels []string, variantAPercent int, spec segment.Spec, kept bool, asOf time.Time, parent string) (queued, skipped int, err error) {
	runID, err := openRun(ctx, tx, id, startedAt)
	if err != nil {
		return 0, 0, err
	}
	queued, skipped, err = insertDeliveries(ctx, tx, id, runID, rule, version, channels, variantAPercent, spec, kept, asOf, parent)
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE campaign_runs SET queued = $2, skipped = $3 WHERE id = $1`, runID, queued, skipped); err != nil {
		return 0, 0, fmt.Errorf("record run size: %w", err)
	}
	return queued, skipped, nil
}

// insertDeliveries queues the people the rule finds at asOf or, with kept, the people
// approval kept. Those were settled then, except that a message sent to one of them
// since must still hold back a campaign that promised to leave recent ones alone.
// Someone an earlier run of the campaign still is reaching on a channel is left out of
// it, and so is someone it already reached, unless the rule lets them in again and
// they have waited as long as it asks. One who was skipped, say for lack of consent,
// was never reached.
func insertDeliveries(ctx context.Context, tx pgx.Tx, id, runID string, rule schedule.Spec, version int, channels []string, variantAPercent int, spec segment.Spec, kept bool, asOf time.Time, parent string) (queued, skipped int, err error) {
	where, args := segment.Compile(spec, asOf)
	if kept {
		where, args = "TRUE", nil
		if spec.NotMessagedWithinDays != nil {
			where, args = segment.Compile(segment.Spec{NotMessagedWithinDays: spec.NotMessagedWithinDays}, asOf)
		}
	}
	campaignParam := fmt.Sprintf("$%d", len(args)+1)
	versionParam := fmt.Sprintf("$%d", len(args)+2)
	traceParam := fmt.Sprintf("$%d", len(args)+3)
	channelsParam := fmt.Sprintf("$%d", len(args)+4)
	testParam := fmt.Sprintf("$%d", len(args)+5)
	runParam := fmt.Sprintf("$%d", len(args)+6)
	args = append(args, id, version, parent, channels, variantAPercent, runID)
	reached := `reached.status <> 'SKIPPED'`
	if wait, again := rule.ReachesAgainAfter(); again {
		reached = `(reached.status IN ('QUEUED', 'SENDING', 'RETRYING') OR (reached.status <> 'SKIPPED' AND reached.updated_at > ` +
			fmt.Sprintf("$%d", len(args)+1) + `::timestamptz))`
		args = append(args, asOf.Add(-wait))
	}
	if kept {
		where += ` AND u.id IN (
			SELECT r.user_id FROM campaign_recipients r
			JOIN campaign_versions v ON v.id = r.campaign_version_id
			WHERE v.campaign_id = ` + campaignParam + `::uuid AND v.version = ` + versionParam + `::int)`
	}

	// The compiled conditions are the fixed fragments from segment.Compile, and
	// all of their values stay parameters.
	err = tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO campaign_deliveries (campaign_id, run_id, campaign_version_id, user_id, channel, idempotency_key, status, last_error, trace_context, variant)
			SELECT `+campaignParam+`::uuid, `+runParam+`::uuid,
			       (SELECT v.id FROM campaign_versions v WHERE v.campaign_id = `+campaignParam+`::uuid AND v.version = `+versionParam+`::int),
			       u.id, ch.channel, `+runParam+`::uuid::text || ':' || u.id || ':' || ch.channel,
			       CASE WHEN consent.allowed THEN 'QUEUED' ELSE 'SKIPPED' END,
			       CASE WHEN consent.allowed THEN NULL ELSE '`+reasonWithoutConsent+`' END,
			       NULLIF(`+traceParam+`::text, ''),
			       CASE WHEN `+testParam+`::int > 0 THEN `+variantOf(campaignParam, testParam)+` END
			FROM users u
			CROSS JOIN unnest(`+channelsParam+`::text[]) AS ch (channel)
			LEFT JOIN consents consent ON consent.user_id = u.id AND consent.channel = ch.channel
			WHERE `+where+`
			  AND NOT EXISTS (
				SELECT 1 FROM campaign_deliveries reached
				WHERE reached.campaign_id = `+campaignParam+`::uuid AND reached.user_id = u.id
				  AND reached.channel = ch.channel AND `+reached+`)
			RETURNING status
		)
		SELECT count(*) FILTER (WHERE status = 'QUEUED'), count(*) FILTER (WHERE status = 'SKIPPED') FROM inserted`, args...).Scan(&queued, &skipped)
	if err != nil {
		return 0, 0, fmt.Errorf("queue deliveries: %w", err)
	}
	return queued, skipped, nil
}

// Progress is a campaign's deliveries added up, for the live view and the report.
type Progress struct {
	Total     int                       `json:"total"`
	Processed int                       `json:"processed"`
	Attempts  int                       `json:"attempts"`
	ByStatus  map[string]int            `json:"by_status"`
	ByChannel map[string]map[string]int `json:"by_channel"`
	// Responses counts provider responses by HTTP code across every attempt.
	Responses map[string]int `json:"responses"`
}

// Progress adds up a campaign's deliveries.
// LiveState is what a page watching a campaign needs to stay current.
type LiveState struct {
	Status   string   `json:"status"`
	Progress Progress `json:"progress"`
	// Alerts are the channel alerts open on the campaign, oldest first.
	Alerts []ChannelAlert `json:"alerts"`
}

// ChannelAlert says one channel of a sending campaign is in trouble while the
// campaign keeps going: most calls failing, nothing accepted while retries wait, or
// credentials refused. The worker opens and closes it.
type ChannelAlert struct {
	Channel string         `json:"channel"`
	Kind    string         `json:"kind"`
	Since   time.Time      `json:"since"`
	Details map[string]any `json:"details"`
}

// openAlerts reads the alerts whose latest event on the campaign is an opening.
func (s *Service) openAlerts(ctx context.Context, id string) ([]ChannelAlert, error) {
	rows, err := s.db.Query(ctx, `
		SELECT channel, kind, occurred_at, details FROM (
			SELECT DISTINCT ON (details->>'channel', details->>'alert')
			       details->>'channel' AS channel, details->>'alert' AS kind, event_type, occurred_at, details
			FROM audit_events
			WHERE campaign_id = $1 AND event_type IN ('campaign.alert_opened', 'campaign.alert_closed')
			ORDER BY details->>'channel', details->>'alert', id DESC
		) latest
		WHERE event_type = 'campaign.alert_opened'
		ORDER BY occurred_at`, id)
	if err != nil {
		return nil, fmt.Errorf("read channel alerts: %w", err)
	}
	alerts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ChannelAlert])
	if err != nil {
		return nil, fmt.Errorf("read channel alerts: %w", err)
	}
	return alerts, nil
}

// Live reads the current state. It says whether the state can still change on its
// own, which is only true from approval until sending ends.
func (s *Service) Live(ctx context.Context, id string) (LiveState, bool, error) {
	view, err := s.load(ctx, id)
	if err != nil {
		return LiveState{}, false, err
	}
	progress, err := s.Progress(ctx, id)
	if err != nil {
		return LiveState{}, false, err
	}
	changing := view.Status == statusApproved || view.Status == statusScheduled || view.Status == statusDispatching || view.Status == statusPaused
	alerts := []ChannelAlert{}
	if view.Status == statusDispatching || view.Status == statusPaused {
		if alerts, err = s.openAlerts(ctx, id); err != nil {
			return LiveState{}, false, err
		}
	}
	return LiveState{view.Status, progress, alerts}, changing, nil
}

// liveFresh is how long a reading of a campaign's live state is shared. Every open
// page of a campaign asks once a second, so without sharing the reads grow with the
// pages; with it, a campaign is read at most twice a second however many watch it.
const liveFresh = 500 * time.Millisecond

// liveCache keeps the last live reading of each campaign. Callers that arrive while a
// campaign is being read wait for that reading instead of starting their own.
type liveCache struct {
	mu       sync.Mutex
	readings map[string]*liveReading
	sweptAt  time.Time
}

type liveReading struct {
	mu       sync.Mutex
	state    LiveState
	changing bool
	readAt   time.Time
}

// SharedLive is Live for a page that asks again and again: a reading from the last
// liveFresh is reused. A page's first reading should use Live, so it starts from now.
func (s *Service) SharedLive(ctx context.Context, id string) (LiveState, bool, error) {
	cache := &s.live
	cache.mu.Lock()
	if cache.readings == nil {
		cache.readings = map[string]*liveReading{}
	}
	// Readings of campaigns nobody watches any more are dropped now and then.
	if time.Since(cache.sweptAt) > time.Minute {
		for key, old := range cache.readings {
			if old.mu.TryLock() {
				if time.Since(old.readAt) > time.Minute {
					delete(cache.readings, key)
				}
				old.mu.Unlock()
			}
		}
		cache.sweptAt = time.Now()
	}
	reading, ok := cache.readings[id]
	if !ok {
		reading = &liveReading{}
		cache.readings[id] = reading
	}
	cache.mu.Unlock()

	reading.mu.Lock()
	defer reading.mu.Unlock()
	if !reading.readAt.IsZero() && time.Since(reading.readAt) < liveFresh {
		return reading.state, reading.changing, nil
	}
	state, changing, err := s.Live(ctx, id)
	if err != nil {
		return LiveState{}, false, err
	}
	reading.state, reading.changing, reading.readAt = state, changing, time.Now()
	return state, changing, nil
}

func (s *Service) Progress(ctx context.Context, id string) (Progress, error) {
	return s.RunProgress(ctx, id, 0)
}

// Runs lists a campaign's sends, newest first.
func (s *Service) Runs(ctx context.Context, id string) ([]RunSummary, error) {
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT r.number, r.started_at, r.finished_at, counted.*
		FROM campaign_runs r CROSS JOIN LATERAL (`+runCounts+`) counted
		WHERE r.campaign_id = $1
		ORDER BY r.number DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	runs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[RunSummary])
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return runs, nil
}

// runID finds run number of a campaign. Number 0 means every run, given as nil.
func (s *Service) runID(ctx context.Context, id string, number int) (*string, error) {
	if number < 0 {
		return nil, ErrUnknownFilter
	}
	if number == 0 {
		return nil, nil //nolint:nilnil // every run is a normal answer
	}
	var run string
	err := s.db.QueryRow(ctx, `SELECT id::text FROM campaign_runs WHERE campaign_id = $1 AND number = $2`, id, number).Scan(&run)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find run: %w", err)
	}
	return &run, nil
}

// RunProgress adds up the deliveries of one run, or of every run when number is 0.
func (s *Service) RunProgress(ctx context.Context, id string, number int) (Progress, error) {
	if _, err := s.load(ctx, id); err != nil {
		return Progress{}, err
	}
	run, err := s.runID(ctx, id, number)
	if err != nil {
		return Progress{}, err
	}
	progress := Progress{ByStatus: map[string]int{}, ByChannel: map[string]map[string]int{}, Responses: map[string]int{}}
	for _, channel := range channels {
		progress.ByChannel[channel] = map[string]int{}
	}

	// Finished runs kept their counts; only runs still sending are counted. One
	// statement, so a run finishing meanwhile is counted once.
	rows, err := s.db.Query(ctx, `
		SELECT c->>0, c->>1, (c->>2)::int, (c->>3)::int
		FROM campaign_runs r, jsonb_array_elements(r.counts) c
		WHERE r.campaign_id = $1 AND r.finished_at IS NOT NULL AND ($2::uuid IS NULL OR r.id = $2)
		UNION ALL
		SELECT d.channel, d.status, count(*)::int, sum(d.attempts)::int
		FROM campaign_runs r JOIN campaign_deliveries d ON d.run_id = r.id
		WHERE r.campaign_id = $1 AND r.finished_at IS NULL AND ($2::uuid IS NULL OR r.id = $2)
		GROUP BY d.channel, d.status`, id, run)
	if err != nil {
		return Progress{}, fmt.Errorf("read progress: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			channel, status string
			count, attempts int
		)
		if err := rows.Scan(&channel, &status, &count, &attempts); err != nil {
			return Progress{}, fmt.Errorf("read progress: %w", err)
		}
		progress.Total += count
		progress.Attempts += attempts
		progress.ByStatus[status] += count
		progress.ByChannel[channel][status] += count
		if slices.Contains(terminalStatuses, status) {
			progress.Processed += count
		}
	}
	if err := rows.Err(); err != nil {
		return Progress{}, fmt.Errorf("read progress: %w", err)
	}

	responses, err := s.db.Query(ctx, `
		SELECT code, sum(calls)::int FROM (
			SELECT e.key AS code, e.value::int AS calls
			FROM campaign_runs r, jsonb_each_text(r.responses) e
			WHERE r.campaign_id = $1 AND r.finished_at IS NOT NULL AND ($2::uuid IS NULL OR r.id = $2)
			UNION ALL
			SELECT a.response_code::text, count(*)::int
			FROM campaign_runs r JOIN delivery_attempts a ON a.run_id = r.id
			WHERE r.campaign_id = $1 AND r.finished_at IS NULL AND a.response_code IS NOT NULL AND ($2::uuid IS NULL OR r.id = $2)
			GROUP BY a.response_code
		) s GROUP BY code`, id, run)
	if err != nil {
		return Progress{}, fmt.Errorf("read responses: %w", err)
	}
	defer responses.Close()
	for responses.Next() {
		var (
			code  string
			count int
		)
		if err := responses.Scan(&code, &count); err != nil {
			return Progress{}, fmt.Errorf("read responses: %w", err)
		}
		progress.Responses[code] = count
	}
	return progress, responses.Err()
}

var terminalStatuses = []string{"SENT", "FAILED", "SKIPPED", "UNKNOWN"}

// DeliveryFilter narrows the delivery list. Empty values mean everything.
type DeliveryFilter struct {
	Status  string
	Channel string
	// Reason is why a delivery ended.
	Reason string
	// Outcome and the window select deliveries by their provider calls, so one
	// accepted after a 429 is still found under rate_limited.
	Outcome string
	From    *time.Time
	To      *time.Time
	// Calls keeps deliveries by how many provider calls they had: "2" for exactly two,
	// "2+" for two or more.
	Calls string
	// City keeps people from one city.
	City string
	// Opened is "yes" or "no" to keep those a provider did or did not report opened.
	Opened string
	// Arrival keeps those a provider reported "delivered" or "bounced", or "waiting"
	// for accepted messages with no report yet.
	Arrival string
	// Variant keeps those who got one version of a test.
	Variant string
	// Run keeps one send of the campaign by its number; 0 keeps every run.
	Run    int
	Limit  int
	Offset int
}

var arrivals = []string{"delivered", "bounced", "waiting"}

// Delivery is one person on one channel. Contact details stay out of it, since
// reading the report does not require them.
type Delivery struct {
	ID        string `json:"id"`
	UserID    int64  `json:"user_id"`
	FirstName string `json:"first_name"`
	City      string `json:"city"`
	Channel   string `json:"channel"`
	Status    string `json:"status"`
	Variant   string `json:"variant"`
	Attempts  int    `json:"attempts"`
	// Calls counts every call to the provider. Attempts leaves out those answered 429.
	Calls             int        `json:"calls"`
	ResponseCode      *int       `json:"response_code"`
	Reason            string     `json:"reason"`
	ProviderMessageID string     `json:"provider_message_id"`
	NextAttemptAt     time.Time  `json:"next_attempt_at"`
	SentAt            *time.Time `json:"sent_at"`
	DeliveredAt       *time.Time `json:"delivered_at"`
	BouncedAt         *time.Time `json:"bounced_at"`
	OpenedAt          *time.Time `json:"opened_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Deliveries lists a campaign's deliveries by person.
// callRange reads a calls filter as the fewest and most calls it keeps. No filter gives
// a negative fewest.
func callRange(calls string) (int, int, error) {
	if calls == "" {
		return -1, 0, nil
	}
	exact, orMore := strings.CutSuffix(calls, "+")
	count, err := strconv.Atoi(exact)
	if err != nil || count < 0 || count > maxCallsFilter {
		return 0, 0, ErrUnknownFilter
	}
	if orMore {
		return count, math.MaxInt32, nil
	}
	return count, count, nil
}

// DeliveryCities lists the cities a campaign's deliveries went to, for filtering by.
func (s *Service) DeliveryCities(ctx context.Context, id string) ([]string, error) {
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT u.city FROM campaign_deliveries d JOIN users u ON u.id = d.user_id
		WHERE d.campaign_id = $1 ORDER BY u.city`, id)
	if err != nil {
		return nil, fmt.Errorf("delivery cities: %w", err)
	}
	cities, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("delivery cities: %w", err)
	}
	return cities, nil
}

// ExportDeliveries hands over every delivery the filter keeps, not one page, and leaves
// a record of who took them. It stops at the first error from write.
func (s *Service) ExportDeliveries(ctx context.Context, id string, filter DeliveryFilter, write func(Delivery) error) error {
	filter.Limit, filter.Offset = maxDeliveryLimit, 0
	for {
		page, err := s.Deliveries(ctx, id, filter)
		if err != nil {
			return err
		}
		if filter.Offset == 0 {
			if err := s.recordExport(ctx, id); err != nil {
				return err
			}
		}
		for _, delivery := range page {
			if err := write(delivery); err != nil {
				return err
			}
		}
		if len(page) < maxDeliveryLimit {
			return nil
		}
		filter.Offset += maxDeliveryLimit
	}
}

func (s *Service) recordExport(ctx context.Context, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin export: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := recordAudit(ctx, tx, id, eventExported, map[string]any{}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit export: %w", err)
	}
	return nil
}

// matching gives the FROM and WHERE of the deliveries a filter keeps, shared by the
// list and its total, with their arguments. It reads only the tables the filter
// needs, so an unfiltered total counts from the campaign's index instead of joining
// people and events for every delivery.
func (f DeliveryFilter) matching(id string) (string, []any, error) {
	if f.Status != "" && !slices.Contains(deliveryStatuses, f.Status) {
		return "", nil, ErrUnknownFilter
	}
	if f.Channel != "" && !slices.Contains(channels, f.Channel) {
		return "", nil, ErrUnknownFilter
	}
	if f.Outcome != "" && !slices.Contains(attemptOutcomes, f.Outcome) {
		return "", nil, ErrUnknownFilter
	}
	if f.Opened != "" && f.Opened != "yes" && f.Opened != "no" {
		return "", nil, ErrUnknownFilter
	}
	if f.Arrival != "" && !slices.Contains(arrivals, f.Arrival) {
		return "", nil, ErrUnknownFilter
	}
	if f.Variant != "" && f.Variant != "A" && f.Variant != "B" {
		return "", nil, ErrUnknownFilter
	}
	if f.Run < 0 {
		return "", nil, ErrUnknownFilter
	}
	fewestCalls, mostCalls, err := callRange(f.Calls)
	if err != nil {
		return "", nil, err
	}

	args := []any{id}
	param := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	from := " FROM campaign_deliveries d"
	where := []string{"d.campaign_id = $1"}
	if f.Run > 0 {
		// The run belongs to the campaign, so its id alone picks the rows. With the
		// campaign as well, PostgreSQL combines two indexes and reads the table.
		where = []string{"d.run_id = (SELECT r.id FROM campaign_runs r WHERE r.campaign_id = $1 AND r.number = " + param(f.Run) + ")"}
	}
	if f.Status != "" {
		where = append(where, "d.status = "+param(f.Status))
	}
	if f.Channel != "" {
		where = append(where, "d.channel = "+param(f.Channel))
	}
	if f.Reason != "" {
		where = append(where, "d.last_error = "+param(f.Reason))
	}
	if f.Variant != "" {
		where = append(where, "d.variant = "+param(f.Variant))
	}
	if f.City != "" {
		from += " JOIN users u ON u.id = d.user_id"
		where = append(where, "u.city = "+param(f.City))
	}
	if f.Opened != "" {
		opened := "EXISTS (SELECT 1 FROM delivery_events e WHERE e.delivery_id = d.id AND e.event_type = 'opened')"
		if f.Opened == "no" {
			opened = "NOT " + opened
		}
		where = append(where, opened)
	}
	if f.Arrival != "" {
		from += " " + arrivalLateral
		switch f.Arrival {
		case "delivered":
			where = append(where, "arrival.delivered_at IS NOT NULL")
		case "bounced":
			where = append(where, "arrival.bounced_at IS NOT NULL")
		case "waiting":
			where = append(where, "d.status = 'SENT' AND arrival.delivered_at IS NULL AND arrival.bounced_at IS NULL")
		}
	}
	if fewestCalls >= 0 {
		where = append(where, "(SELECT count(*) FROM delivery_attempts a WHERE a.delivery_id = d.id) BETWEEN "+param(fewestCalls)+" AND "+param(mostCalls))
	}
	if f.Outcome != "" || f.From != nil || f.To != nil {
		calls := []string{"a.delivery_id = d.id"}
		if f.Outcome != "" {
			calls = append(calls, "a.outcome = "+param(f.Outcome))
		}
		if f.From != nil {
			calls = append(calls, "a.occurred_at >= "+param(*f.From))
		}
		if f.To != nil {
			calls = append(calls, "a.occurred_at < "+param(*f.To))
		}
		where = append(where, "EXISTS (SELECT 1 FROM delivery_attempts a WHERE "+strings.Join(calls, " AND ")+")")
	}
	return from + " WHERE " + strings.Join(where, " AND "), args, nil
}

// CountDeliveries says how many deliveries a filter keeps across every page.
func (s *Service) CountDeliveries(ctx context.Context, id string, filter DeliveryFilter) (int, error) {
	matching, args, err := filter.matching(id)
	if err != nil {
		return 0, err
	}
	if _, err := s.load(ctx, id); err != nil {
		return 0, err
	}
	var total int
	if filter.keepsEverything() {
		// A finished run kept its total, and no delivery joins or leaves a run after it
		// opens, so only runs still sending are counted.
		runs, runArgs := "r.campaign_id = $1", []any{id}
		if filter.Run > 0 {
			runs, runArgs = runs+" AND r.number = $2", append(runArgs, filter.Run)
		}
		err = s.db.QueryRow(ctx, `
			SELECT COALESCE(sum(CASE WHEN r.finished_at IS NOT NULL THEN r.total
			                         ELSE (SELECT count(*) FROM campaign_deliveries d WHERE d.run_id = r.id) END), 0)::int
			FROM campaign_runs r WHERE `+runs, runArgs...).Scan(&total)
	} else {
		err = s.db.QueryRow(ctx, "SELECT count(*)"+matching, args...).Scan(&total)
	}
	if err != nil {
		return 0, fmt.Errorf("count deliveries: %w", err)
	}
	return total, nil
}

// keepsEverything says the filter picks every delivery of the campaign or of one run.
// Any condition, including one added later, makes it false.
func (f DeliveryFilter) keepsEverything() bool {
	f.Run, f.Limit, f.Offset = 0, 0, 0
	return f == DeliveryFilter{}
}

func (s *Service) Deliveries(ctx context.Context, id string, filter DeliveryFilter) ([]Delivery, error) {
	matching, args, err := filter.matching(id)
	if err != nil {
		return nil, err
	}
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultDeliveryLimit
	}
	limit = min(limit, maxDeliveryLimit)

	rows, err := s.db.Query(ctx, `
		SELECT d.id::text, d.user_id, u.first_name, u.city, d.channel, d.status, COALESCE(d.variant, ''), d.attempts,
		       (SELECT count(*) FROM delivery_attempts a WHERE a.delivery_id = d.id), last.response_code,
		       COALESCE(d.last_error, ''), COALESCE(d.provider_message_id, ''), d.next_attempt_at, d.sent_at, arrival.delivered_at, arrival.bounced_at, opened.occurred_at, d.updated_at
		FROM (SELECT d.id`+matching+`
			ORDER BY d.user_id, d.channel
			LIMIT `+fmt.Sprintf("$%d OFFSET $%d", len(args)+1, len(args)+2)+`) page
		JOIN campaign_deliveries d ON d.id = page.id
		JOIN users u ON u.id = d.user_id
		LEFT JOIN delivery_events opened ON opened.delivery_id = d.id AND opened.event_type = 'opened'
		`+arrivalLateral+`
		LEFT JOIN LATERAL (
			SELECT response_code FROM delivery_attempts WHERE delivery_id = d.id ORDER BY id DESC LIMIT 1
		) last ON true
		ORDER BY d.user_id, d.channel`, append(args, limit, max(filter.Offset, 0))...)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	deliveries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Delivery, error) {
		var d Delivery
		err := row.Scan(&d.ID, &d.UserID, &d.FirstName, &d.City, &d.Channel, &d.Status, &d.Variant, &d.Attempts, &d.Calls, &d.ResponseCode,
			&d.Reason, &d.ProviderMessageID, &d.NextAttemptAt, &d.SentAt, &d.DeliveredAt, &d.BouncedAt, &d.OpenedAt, &d.UpdatedAt)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	return deliveries, nil
}

// Attempt is one call to the provider.
type Attempt struct {
	Attempt      int       `json:"attempt"`
	ResponseCode *int      `json:"response_code"`
	Outcome      string    `json:"outcome"`
	Error        string    `json:"error"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// DeliveryAttempts returns one delivery's history, oldest first.
func (s *Service) DeliveryAttempts(ctx context.Context, id, deliveryID string) ([]Attempt, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(deliveryID); err != nil {
		return nil, ErrNotFound
	}
	var exists bool
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaign_deliveries WHERE id = $1 AND campaign_id = $2)`, parsed, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("find delivery: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.db.Query(ctx, `SELECT attempt, response_code, outcome, COALESCE(error, ''), occurred_at FROM delivery_attempts WHERE delivery_id = $1 ORDER BY id`, parsed)
	if err != nil {
		return nil, fmt.Errorf("read attempts: %w", err)
	}
	attempts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Attempt, error) {
		var a Attempt
		err := row.Scan(&a.Attempt, &a.ResponseCode, &a.Outcome, &a.Error, &a.OccurredAt)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("read attempts: %w", err)
	}
	return attempts, nil
}

// checkVersions holds every message to the rules, and to each other: neither a
// person's language nor their side of a test may change what they are offered.
func checkVersions(view *View) error {
	languages := view.writingLanguages()
	// A draft from before languages were kept covers only the audience's largest one.
	if view.Content.Language == "" {
		if len(languages) > 1 {
			return ErrMissingLanguage
		}
		return checkVersion(view, view.Content)
	}
	if view.ABTest && view.Content.VariantB == nil {
		return ErrMissingVariant
	}
	for _, root := range view.Content.variants() {
		for _, language := range languages {
			if root.version(language) == nil {
				return ErrMissingLanguage
			}
		}
		for _, version := range root.all() {
			if err := checkVersion(view, version); err != nil {
				return err
			}
			if content.OffersDiffer(view.Content.Content, version.Content) {
				return ErrLanguageDiscount
			}
		}
	}
	return nil
}

func checkVersion(view *View, version *StoredContent) error {
	if issues := content.CheckMessages(version.Content, view.limitsFor(version.Language)); len(issues) > 0 {
		return &content.ValidationError{Issues: issues}
	}
	if content.DiscountsDiffer(version.Content) {
		return ErrChannelDiscount
	}
	return nil
}
