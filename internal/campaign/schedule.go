package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/negativexq/smart-campaign-composer/internal/schedule"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

const (
	statusScheduled    = "SCHEDULED"
	eventScheduled     = "campaign.scheduled"
	eventScheduleEnded = "campaign.schedule_cancelled"
	eventRun           = "campaign.run"
	schedulerInterval  = time.Second
)

// Errors the API maps to responses.
var (
	ErrAlreadyScheduled = errors.New("campaign is scheduled")
	ErrNoSchedule       = errors.New("campaign has no schedule to cancel")
)

// ScheduleView is a campaign's sending rule and where it stands.
type ScheduleView struct {
	Spec        schedule.Spec `json:"spec"`
	Description string        `json:"description"`
	NextRunAt   *time.Time    `json:"next_run_at"`
	CreatedBy   *string       `json:"created_by"`
	LastRunAt   *time.Time    `json:"last_run_at"`
	Runs        int           `json:"runs"`
}

// Schedule sets when an approved campaign sends, replacing any earlier rule.
func (s *Service) Schedule(ctx context.Context, id string, spec schedule.Spec) (View, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin schedule: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	status, err := lockStatus(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if status != statusApproved && status != statusScheduled {
		return View{}, ErrNotApproved
	}
	now := s.now()
	if err := spec.Validate(now); err != nil {
		return View{}, err
	}
	if spec.Kind == schedule.KindInterval && spec.StartsAt == nil {
		spec.StartsAt = &now
	}
	next, ok := spec.Next(now)
	if !ok {
		return View{}, &schedule.InvalidError{Message: "This schedule would never send anything."}
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return View{}, fmt.Errorf("encode schedule: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO campaign_schedules (campaign_id, spec, next_run_at, created_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (campaign_id) DO UPDATE SET spec = EXCLUDED.spec, next_run_at = EXCLUDED.next_run_at, created_by = EXCLUDED.created_by`, id, encoded, next, personFrom(ctx)); err != nil {
		return View{}, fmt.Errorf("save schedule: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, statusScheduled); err != nil {
		return View{}, fmt.Errorf("mark scheduled: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventScheduled, map[string]any{"schedule": spec.Describe(), "next_run_at": next}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit schedule: %w", err)
	}
	return s.Get(ctx, id)
}

// CancelSchedule stops future runs. A campaign that never sent goes back to being
// approved; one that did finishes with what it has sent.
func (s *Service) CancelSchedule(ctx context.Context, id string) (View, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin cancel: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	status, err := lockStatus(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if status != statusScheduled && status != statusDispatching && status != statusPaused {
		return View{}, ErrNoSchedule
	}
	var pending, sentBefore bool
	err = tx.QueryRow(ctx, `
		SELECT next_run_at IS NOT NULL, runs > 0 FROM campaign_schedules WHERE campaign_id = $1`, id).Scan(&pending, &sentBefore)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !pending) {
		return View{}, ErrNoSchedule
	}
	if err != nil {
		return View{}, fmt.Errorf("read schedule: %w", err)
	}
	next := statusDispatching // the worker closes it once its deliveries are done
	if status == statusPaused {
		next = statusPaused // cancelling later runs does not resume this one
	}
	if sentBefore {
		_, err = tx.Exec(ctx, `UPDATE campaign_schedules SET next_run_at = NULL WHERE campaign_id = $1`, id)
	} else {
		next = statusApproved
		_, err = tx.Exec(ctx, `DELETE FROM campaign_schedules WHERE campaign_id = $1`, id)
	}
	if err != nil {
		return View{}, fmt.Errorf("cancel schedule: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, next); err != nil {
		return View{}, fmt.Errorf("update status: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventScheduleEnded, map[string]any{}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit cancel: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Service) loadSchedule(ctx context.Context, q segment.Querier, id string) (*ScheduleView, error) {
	var (
		view    ScheduleView
		encoded []byte
	)
	err := q.QueryRow(ctx, `SELECT spec, next_run_at, last_run_at, runs, created_by FROM campaign_schedules WHERE campaign_id = $1`, id).
		Scan(&encoded, &view.NextRunAt, &view.LastRunAt, &view.Runs, &view.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // no schedule is a normal state
	}
	if err != nil {
		return nil, fmt.Errorf("load schedule: %w", err)
	}
	if err := json.Unmarshal(encoded, &view.Spec); err != nil {
		return nil, fmt.Errorf("decode schedule: %w", err)
	}
	view.Description = view.Spec.Describe()
	return &view, nil
}

// Scheduler starts the runs that are due. Any number can run side by side: a run
// and the move to its next time commit together under a row lock.
type Scheduler struct {
	db     *pgxpool.Pool
	tracer trace.Tracer
	now    func() time.Time
}

// NewScheduler builds a scheduler.
func NewScheduler(db *pgxpool.Pool, tracer trace.Tracer, now func() time.Time) *Scheduler {
	return &Scheduler{db, tracer, now}
}

// Run checks for due schedules until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.RunDue(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "run schedules", "error", err)
			}
		}
	}
}

// RunDue starts every due run and says how many it started.
func (s *Scheduler) RunDue(ctx context.Context) (int, error) {
	ran := 0
	for {
		started, err := s.runOne(ctx)
		if err != nil {
			return ran, err
		}
		if !started {
			return ran, nil
		}
		ran++
	}
}

func (s *Scheduler) runOne(ctx context.Context) (bool, error) {
	now := s.now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin run: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var (
		id, status       string
		version          int
		encodedSpec, seg []byte
		scheduledFor     time.Time
		scheduledBy      *string
		channels         []string
		abTest           bool
		variantAPercent  int
		kept             bool
	)
	err = tx.QueryRow(ctx, `
		SELECT c.id::text, c.status, v.version, s.spec, v.segment, s.next_run_at, s.created_by, c.channels, c.ab_test, c.variant_a_percent, v.recipients_kept
		FROM campaign_schedules s
		JOIN campaigns c ON c.id = s.campaign_id
		JOIN campaign_versions v ON v.campaign_id = c.id AND v.approved_at IS NOT NULL
		WHERE s.next_run_at <= $1 AND c.status IN ('SCHEDULED', 'DISPATCHING')
		ORDER BY s.next_run_at
		LIMIT 1
		FOR UPDATE OF s, c SKIP LOCKED`, now).Scan(&id, &status, &version, &encodedSpec, &seg, &scheduledFor, &scheduledBy, &channels, &abTest, &variantAPercent, &kept)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find due schedule: %w", err)
	}

	ctx, span := s.tracer.Start(ctx, "campaign.run", trace.WithAttributes(attribute.String("campaign.id", id)))
	defer span.End()

	var spec schedule.Spec
	if err := json.Unmarshal(encodedSpec, &spec); err != nil {
		return false, fmt.Errorf("decode schedule: %w", err)
	}
	var proposal segment.Proposal
	if err := json.Unmarshal(seg, &proposal); err != nil {
		return false, fmt.Errorf("decode segment: %w", err)
	}
	// A one-time send goes to the people approved. A repeating one finds who matches
	// now, leaving out everyone an earlier run already reached.
	queued, skipped, err := queueRun(ctx, tx, id, now, version, channels, testShare(abTest, variantAPercent), proposal.Segment, kept && spec.Kind == schedule.KindOnce, now, traceParent(ctx))
	if err != nil {
		return false, err
	}

	var nextRun *time.Time
	if next, ok := spec.Next(now); ok {
		nextRun = &next
	}
	if _, err := tx.Exec(ctx, `UPDATE campaign_schedules SET next_run_at = $2, last_run_at = $3, runs = runs + 1 WHERE campaign_id = $1`, id, nextRun, now); err != nil {
		return false, fmt.Errorf("advance schedule: %w", err)
	}
	// With nobody new and more runs to come, there is nothing to wait for.
	if queued > 0 || nextRun == nil {
		status = statusDispatching
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, status); err != nil {
		return false, fmt.Errorf("start run: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventRun, map[string]any{"scheduled_for": scheduledFor, "scheduled_by": scheduledBy, "queued": queued, "skipped": skipped}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit run: %w", err)
	}
	return true, nil
}
