package campaign

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

const (
	statusApproved    = "APPROVED"
	statusDispatching = "DISPATCHING"
	eventApproved     = "campaign.approved"

	eventDispatched      = "campaign.dispatched"
	reasonWithoutConsent = "no_consent"

	defaultDeliveryLimit = 200
	maxDeliveryLimit     = 500
)

// Errors the API maps to responses.
var (
	ErrNotReviewable = errors.New("campaign has no messages ready for review")
	ErrNotApproved   = errors.New("campaign has not been approved")
	ErrUnknownFilter = errors.New("unknown delivery filter")
)

var (
	deliveryStatuses = []string{"QUEUED", "SENDING", "SENT", "RETRYING", "FAILED", "SKIPPED", "UNKNOWN"}
	channels         = []string{"sms", "email"}
)

// lockStatus takes the campaign's row lock, which edits and generation also need,
// so what is checked under it cannot change before the transaction ends.
func lockStatus(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return "", ErrNotFound
	}
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1 FOR UPDATE`, parsed).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock campaign: %w", err)
	}
	return status, nil
}

// Approve is the human decision that freezes what will be sent. Everything is
// checked again here, because users and consents may have changed during review.
func (s *Service) Approve(ctx context.Context, id string) (View, error) {
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
	case statusApproved, statusScheduled, statusDispatching, "COMPLETED", "COMPLETED_WITH_FAILURES":
		return s.Get(ctx, id)
	default:
		return View{}, ErrNotReviewable
	}
	view, err := s.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if view.Content == nil {
		return View{}, ErrNotReviewable
	}
	if len(view.Blockers) > 0 {
		return View{}, ErrAudienceBlocked
	}
	if issues := content.CheckMessages(view.Content.Content, view.ContentLimits); len(issues) > 0 {
		return View{}, &content.ValidationError{Issues: issues}
	}

	if _, err := tx.Exec(ctx, `UPDATE campaign_versions SET approved_at = now() WHERE campaign_id = $1 AND version = $2`, id, view.Version); err != nil {
		return View{}, fmt.Errorf("approve version: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, statusApproved); err != nil {
		return View{}, fmt.Errorf("approve campaign: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventApproved, map[string]any{
		"version": view.Version, "audience": view.Audience, "warnings": len(view.ContentWarnings),
	}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit approve: %w", err)
	}
	return s.Get(ctx, id)
}

// Dispatch fixes the recipients and queues one delivery per person and channel.
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
	case statusDispatching, "COMPLETED", "COMPLETED_WITH_FAILURES":
		return s.Get(ctx, id)
	case statusScheduled:
		return View{}, ErrAlreadyScheduled
	default:
		return View{}, ErrNotApproved
	}
	stored, err := s.load(ctx, id)
	if err != nil {
		return View{}, err
	}

	queued, skipped, err := insertDeliveries(ctx, tx, id, stored.Version, stored.Proposal.Segment, stored.AudienceAsOf, traceParent(ctx))
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

func traceParent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

func insertDeliveries(ctx context.Context, tx pgx.Tx, id string, version int, spec segment.Spec, asOf time.Time, parent string) (queued, skipped int, err error) {
	where, args := segment.Compile(spec, asOf)
	campaignParam := fmt.Sprintf("$%d", len(args)+1)
	versionParam := fmt.Sprintf("$%d", len(args)+2)
	traceParam := fmt.Sprintf("$%d", len(args)+3)
	args = append(args, id, version, parent)

	// The compiled conditions are the fixed fragments from segment.Compile, and
	// all of their values stay parameters.
	err = tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO campaign_deliveries (campaign_id, campaign_version_id, user_id, channel, idempotency_key, status, last_error, trace_context)
			SELECT `+campaignParam+`::uuid,
			       (SELECT v.id FROM campaign_versions v WHERE v.campaign_id = `+campaignParam+`::uuid AND v.version = `+versionParam+`::int),
			       u.id, ch.channel, `+campaignParam+`::uuid::text || ':' || u.id || ':' || ch.channel,
			       CASE WHEN consent.allowed THEN 'QUEUED' ELSE 'SKIPPED' END,
			       CASE WHEN consent.allowed THEN NULL ELSE '`+reasonWithoutConsent+`' END,
			       NULLIF(`+traceParam+`::text, '')
			FROM users u
			CROSS JOIN (VALUES ('sms'), ('email')) AS ch (channel)
			LEFT JOIN consents consent ON consent.user_id = u.id AND consent.channel = ch.channel
			WHERE `+where+`
			ON CONFLICT (campaign_id, user_id, channel) DO UPDATE
			SET status = 'QUEUED', last_error = NULL, next_attempt_at = now(), updated_at = now(), trace_context = EXCLUDED.trace_context
			WHERE campaign_deliveries.status = 'SKIPPED' AND EXCLUDED.status = 'QUEUED'
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
	changing := view.Status == statusApproved || view.Status == statusScheduled || view.Status == statusDispatching
	return LiveState{view.Status, progress}, changing, nil
}

func (s *Service) Progress(ctx context.Context, id string) (Progress, error) {
	if _, err := s.load(ctx, id); err != nil {
		return Progress{}, err
	}
	progress := Progress{ByStatus: map[string]int{}, ByChannel: map[string]map[string]int{}, Responses: map[string]int{}}
	for _, channel := range channels {
		progress.ByChannel[channel] = map[string]int{}
	}

	rows, err := s.db.Query(ctx, `SELECT channel, status, count(*), sum(attempts) FROM campaign_deliveries WHERE campaign_id = $1 GROUP BY channel, status`, id)
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
		progress.ByChannel[channel][status] = count
		if slices.Contains(terminalStatuses, status) {
			progress.Processed += count
		}
	}
	if err := rows.Err(); err != nil {
		return Progress{}, fmt.Errorf("read progress: %w", err)
	}

	responses, err := s.db.Query(ctx, `
		SELECT a.response_code::text, count(*)
		FROM delivery_attempts a JOIN campaign_deliveries d ON d.id = a.delivery_id
		WHERE d.campaign_id = $1 AND a.response_code IS NOT NULL
		GROUP BY a.response_code`, id)
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
	Limit   int
	Offset  int
}

// Delivery is one person on one channel. Contact details stay out of it, since
// reading the report does not require them.
type Delivery struct {
	ID                string     `json:"id"`
	UserID            int64      `json:"user_id"`
	FirstName         string     `json:"first_name"`
	Channel           string     `json:"channel"`
	Status            string     `json:"status"`
	Attempts          int        `json:"attempts"`
	ResponseCode      *int       `json:"response_code"`
	Reason            string     `json:"reason"`
	ProviderMessageID string     `json:"provider_message_id"`
	NextAttemptAt     time.Time  `json:"next_attempt_at"`
	SentAt            *time.Time `json:"sent_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Deliveries lists a campaign's deliveries by person.
func (s *Service) Deliveries(ctx context.Context, id string, filter DeliveryFilter) ([]Delivery, error) {
	if filter.Status != "" && !slices.Contains(deliveryStatuses, filter.Status) {
		return nil, ErrUnknownFilter
	}
	if filter.Channel != "" && !slices.Contains(channels, filter.Channel) {
		return nil, ErrUnknownFilter
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
		SELECT d.id::text, d.user_id, u.first_name, d.channel, d.status, d.attempts, last.response_code,
		       COALESCE(d.last_error, ''), COALESCE(d.provider_message_id, ''), d.next_attempt_at, d.sent_at, d.updated_at
		FROM campaign_deliveries d
		JOIN users u ON u.id = d.user_id
		LEFT JOIN LATERAL (
			SELECT response_code FROM delivery_attempts WHERE delivery_id = d.id ORDER BY id DESC LIMIT 1
		) last ON true
		WHERE d.campaign_id = $1 AND ($2 = '' OR d.status = $2) AND ($3 = '' OR d.channel = $3)
		ORDER BY d.user_id, d.channel
		LIMIT $4 OFFSET $5`, id, filter.Status, filter.Channel, limit, max(filter.Offset, 0))
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	deliveries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Delivery, error) {
		var d Delivery
		err := row.Scan(&d.ID, &d.UserID, &d.FirstName, &d.Channel, &d.Status, &d.Attempts, &d.ResponseCode,
			&d.Reason, &d.ProviderMessageID, &d.NextAttemptAt, &d.SentAt, &d.UpdatedAt)
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
