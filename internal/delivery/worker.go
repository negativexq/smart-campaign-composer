// Package delivery sends queued messages. The queue is the campaign_deliveries
// table: workers claim rows with a lease and record each outcome under that claim,
// so a worker that lost its claim cannot overwrite what another one did.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

const (
	statusSent     = "SENT"
	statusRetrying = "RETRYING"
	statusFailed   = "FAILED"
	statusSkipped  = "SKIPPED"
	statusUnknown  = "UNKNOWN"

	outcomeAccepted    = "accepted"
	outcomeRateLimited = "rate_limited"
	outcomeServerError = "server_error"
	outcomeRejected    = "rejected"
	// outcomeTimeout covers every call that ended without a response: the
	// provider may have the message, so it can never be called a plain failure.
	outcomeTimeout = "timeout"

	reasonLeaseExpired = "lease_expired"
	reasonNotSent      = "provider_unreachable"

	recoverBatch       = 500
	limiterPause       = time.Second
	notSentPause       = 5 * time.Second
	recordTimeout      = 5 * time.Second
	defaultSendTimeout = 5 * time.Second
	finishInterval     = time.Second
	idlePause          = 200 * time.Millisecond
	errorPause         = 2 * time.Second
)

// MaxDrain is the longest a worker can take to stop once told to: a claim already
// under way, then one provider call that gets no answer, pausing the channel and
// recording the outcome. Whatever runs the worker must wait longer than this, or
// messages stay in flight until their lease expires.
const MaxDrain = recordTimeout + defaultSendTimeout + recordTimeout + recordTimeout

// Message is one rendered message for the provider.
type Message struct {
	Channel        string
	To             string
	Subject        string
	Body           string
	HTML           string
	UnsubscribeURL string
	IdempotencyKey string
}

// Result is the provider's answer.
type Result struct {
	StatusCode int
	MessageID  string
	Duplicate  bool
	RetryAfter time.Duration
}

// Sender calls the provider.
type Sender interface {
	Send(ctx context.Context, message Message) (Result, error)
}

// Limiter grants permission to send on a channel.
type Limiter interface {
	Wait(ctx context.Context, channel string) (time.Duration, error)
	Pause(ctx context.Context, channel string, length time.Duration) error
}

// ErrNotSent means the call never reached the provider.
var ErrNotSent = errors.New("message was not sent")

// Config tunes a worker.
type Config struct {
	BatchSize   int
	Concurrency int
	Lease       time.Duration
	MaxAttempts int
	SendTimeout time.Duration
	Backoff     func(attempt int) time.Duration
	// Unsubscribe gives the signed link for a person's emails.
	Unsubscribe func(userID int64) string
}

// DefaultConfig holds production settings.
func DefaultConfig() Config {
	return Config{
		BatchSize:   32,
		Concurrency: 32,
		Lease:       time.Minute,
		MaxAttempts: 5,
		SendTimeout: defaultSendTimeout,
		Backoff:     ExponentialBackoff,
	}
}

// ExponentialBackoff doubles from one second with jitter, so messages that failed
// together do not all come back together.
func ExponentialBackoff(attempt int) time.Duration {
	base := time.Second << min(attempt-1, 6)
	return base + rand.N(base/2)
}

// Worker processes the queue.
type Worker struct {
	db      *pgxpool.Pool
	sender  Sender
	limiter Limiter
	metrics *telemetry.Metrics
	tracer  trace.Tracer
	config  Config
}

// New builds a worker.
func New(db *pgxpool.Pool, sender Sender, limiter Limiter, metrics *telemetry.Metrics, tracer trace.Tracer, config Config) *Worker {
	return &Worker{db, sender, limiter, metrics, tracer, config}
}

// Run works until ctx ends, then lets calls in progress finish and be recorded.
func (w *Worker) Run(ctx context.Context) error {
	var housekeeping sync.WaitGroup
	housekeeping.Go(func() { w.runHousekeeping(ctx) })
	defer housekeeping.Wait()

	for ctx.Err() == nil {
		claimed, err := w.ProcessBatch(ctx)
		pause := time.Duration(0)
		switch {
		case err != nil && ctx.Err() == nil:
			slog.ErrorContext(ctx, "process deliveries", "error", err)
			pause = errorPause
		case claimed == 0:
			pause = idlePause
		}
		select {
		case <-ctx.Done():
		case <-time.After(pause):
		}
	}
	return nil
}

func (w *Worker) runHousekeeping(ctx context.Context) {
	ticker := time.NewTicker(finishInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.RecoverExpired(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "recover deliveries", "error", err)
			}
			if err := w.FinishCampaigns(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "finish campaigns", "error", err)
			}
			if err := w.reportQueueDepth(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "report queue depth", "error", err)
			}
		}
	}
}

type claim struct {
	id, token, campaignID, channel, key string
	userID                              int64
	attempts                            int
	traceContext                        *string
	firstName, email, phone             string
	consented                           bool
	content                             content.Content
}

// ProcessBatch claims what is due and handles it, returning how many it claimed.
func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	claims, err := w.claim(ctx)
	if err != nil {
		return 0, err
	}
	var (
		wg    sync.WaitGroup
		slots = make(chan struct{}, max(w.config.Concurrency, 1))
	)
	for _, item := range claims {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			if err := w.handle(ctx, item); err != nil {
				slog.ErrorContext(ctx, "record delivery", "delivery_id", item.id, "error", err)
			}
		})
	}
	wg.Wait()
	return len(claims), nil
}

// claim outlives a shutdown: giving up on a claim the database has already
// committed would strand those rows as SENDING until their lease expires.
func (w *Worker) claim(ctx context.Context) ([]claim, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	rows, err := w.db.Query(ctx, `
		WITH next AS (
			SELECT d.id
			FROM campaign_deliveries d
			-- A subquery rather than a join: with one campaign sending, a join makes the
			-- planner fetch and sort its whole queue to take a few rows, which took 700 ms
			-- with a million waiting. This way it reads the claimable index in order.
			WHERE d.status IN ('QUEUED', 'RETRYING') AND d.next_attempt_at <= now()
			  AND (SELECT c.status FROM campaigns c WHERE c.id = d.campaign_id) = 'DISPATCHING'
			ORDER BY d.next_attempt_at
			LIMIT $1
			FOR UPDATE OF d SKIP LOCKED
		)
		UPDATE campaign_deliveries d
		SET status = 'SENDING', claim_token = gen_random_uuid(), lease_expires_at = now() + make_interval(secs => $2), updated_at = now()
		FROM next, users u, campaign_versions v
		WHERE d.id = next.id AND u.id = d.user_id AND v.id = d.campaign_version_id
		RETURNING d.id::text, d.claim_token::text, d.campaign_id::text, d.channel, d.idempotency_key, d.user_id, d.attempts, d.trace_context,
		          u.first_name, u.email, u.phone, u.language,
		          COALESCE((SELECT allowed FROM consents WHERE user_id = d.user_id AND channel = d.channel), false),
		          v.content`, w.config.BatchSize, w.config.Lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim deliveries: %w", err)
	}
	claims, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (claim, error) {
		var (
			item     claim
			encoded  []byte
			language string
		)
		if err := row.Scan(&item.id, &item.token, &item.campaignID, &item.channel, &item.key, &item.userID, &item.attempts, &item.traceContext,
			&item.firstName, &item.email, &item.phone, &language, &item.consented, &encoded); err != nil {
			return claim{}, err
		}
		var stored struct {
			content.Content
			Translations []content.Content `json:"translations"`
		}
		if err := json.Unmarshal(encoded, &stored); err != nil {
			return claim{}, fmt.Errorf("decode approved content: %w", err)
		}
		item.content = content.ForLanguage(stored.Content, stored.Translations, language)
		return item, nil
	})
	if err != nil {
		return nil, fmt.Errorf("claim deliveries: %w", err)
	}
	return claims, nil
}

func (c claim) message(unsubscribeURL string) (Message, error) {
	message := Message{Channel: c.channel, IdempotencyKey: c.key}
	if c.channel == "sms" {
		message.To = c.phone
		message.Body = content.Render(c.content.SMS, content.NameFor(c.content.Language, c.firstName))
		return message, nil
	}
	message.To = c.email
	message.UnsubscribeURL = unsubscribeURL
	email, err := content.RenderEmail(c.content, c.firstName, unsubscribeURL)
	if err != nil {
		return Message{}, err
	}
	message.Subject, message.Body, message.HTML = email.Subject, email.Text, email.HTML
	return message, nil
}

func (w *Worker) handle(ctx context.Context, item claim) (err error) {
	ctx, span := w.startSpan(ctx, item)
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "record failed")
		}
		span.End()
	}()

	if !item.consented {
		span.SetAttributes(attribute.String("delivery.outcome", "consent_withdrawn"))
		return w.record(ctx, item, update{status: statusSkipped, attempts: item.attempts, reason: "consent_withdrawn"}, nil)
	}

	limitCtx, limitSpan := w.tracer.Start(ctx, "delivery.limit")
	waited, err := w.limiter.Wait(limitCtx, item.channel)
	limitSpan.End()
	w.metrics.DeliveryRateLimitWait.WithLabelValues(item.channel).Observe(waited.Seconds())
	if err != nil {
		// Nothing was sent, so this costs no attempt.
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "no permission to send", "channel", item.channel, "error", err)
		}
		return w.record(ctx, item, update{status: statusRetrying, attempts: item.attempts, retryIn: limiterPause, reason: "rate_limiter_unavailable"}, nil)
	}

	result, sendErr := w.send(ctx, item)
	if errors.Is(sendErr, ErrNotSent) {
		// Certain that nothing went out, so this costs no attempt and the campaign
		// can wait out a provider outage instead of using up its retries.
		span.SetAttributes(attribute.String("delivery.outcome", reasonNotSent))
		w.metrics.DeliveryAttempts.WithLabelValues(item.channel, reasonNotSent).Inc()
		w.pause(ctx, item.channel, notSentPause)
		return w.record(ctx, item, update{status: statusRetrying, attempts: item.attempts, retryIn: notSentPause, reason: reasonNotSent}, nil)
	}
	outcome, code := classify(result, sendErr)
	span.SetAttributes(attribute.String("delivery.outcome", outcome))
	w.metrics.DeliveryAttempts.WithLabelValues(item.channel, outcome).Inc()
	if result.Duplicate {
		w.metrics.DeliveryDuplicates.WithLabelValues(item.channel).Inc()
	}
	if outcome != outcomeAccepted {
		span.SetStatus(codes.Error, outcome)
	}

	next, err := w.nextStep(ctx, item, outcome, result)
	if err != nil {
		return err
	}
	if next.status == statusRetrying {
		w.metrics.DeliveryRetries.WithLabelValues(item.channel, outcome).Inc()
	}
	if outcome == outcomeRateLimited {
		w.pause(ctx, item.channel, result.RetryAfter)
	}
	attemptError := ""
	if sendErr != nil {
		attemptError = "no response from the provider"
	}
	number := item.attempts + 1
	return w.record(ctx, item, next, &attemptRow{number, code, outcome, attemptError})
}

// pause tells every worker to hold a channel. Failing to do so only means more
// calls get a 429, so it must not stop the outcome from being recorded.
func (w *Worker) pause(ctx context.Context, channel string, length time.Duration) {
	pauseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if err := w.limiter.Pause(pauseCtx, channel, length); err != nil {
		slog.WarnContext(ctx, "pause channel", "channel", channel, "error", err)
	}
}

func (w *Worker) startSpan(ctx context.Context, item claim) (context.Context, trace.Span) {
	options := []trace.SpanStartOption{trace.WithAttributes(
		attribute.String("campaign.id", item.campaignID),
		attribute.String("delivery.id", item.id),
		attribute.String("delivery.channel", item.channel),
		attribute.Int("delivery.attempt", item.attempts+1),
	)}
	if item.traceContext != nil {
		parent := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{"traceparent": *item.traceContext})
		options = append(options, trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(parent)}))
	}
	return w.tracer.Start(ctx, "delivery.send", options...)
}

// send outlives a shutdown: stopping halfway would leave the outcome unknown.
func (w *Worker) send(ctx context.Context, item claim) (Result, error) {
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.SendTimeout)
	defer cancel()
	callCtx, span := w.tracer.Start(callCtx, "delivery.provider")
	defer span.End()

	message, err := item.message(w.unsubscribeURL(item))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrNotSent, err)
	}
	start := time.Now()
	result, err := w.sender.Send(callCtx, message)
	w.metrics.DeliveryDuration.WithLabelValues(item.channel).Observe(time.Since(start).Seconds())
	span.SetAttributes(attribute.Int("http.response.status_code", result.StatusCode))
	return result, err
}

func (w *Worker) unsubscribeURL(item claim) string {
	if item.channel != "email" || w.config.Unsubscribe == nil {
		return ""
	}
	return w.config.Unsubscribe(item.userID)
}

func classify(result Result, sendErr error) (string, *int) {
	if sendErr != nil {
		return outcomeTimeout, nil
	}
	code := result.StatusCode
	switch {
	case code >= 200 && code < 300:
		return outcomeAccepted, &code
	case code == http.StatusTooManyRequests:
		return outcomeRateLimited, &code
	case code >= 500 || code == http.StatusRequestTimeout:
		return outcomeServerError, &code
	default:
		return outcomeRejected, &code
	}
}

type update struct {
	status    string
	attempts  int
	retryIn   time.Duration
	reason    string
	messageID string
}

type attemptRow struct {
	number  int
	code    *int
	outcome string
	err     string
}

func (w *Worker) nextStep(ctx context.Context, item claim, outcome string, result Result) (update, error) {
	used := item.attempts + 1
	switch outcome {
	case outcomeAccepted:
		return update{status: statusSent, attempts: used, messageID: result.MessageID}, nil
	case outcomeRateLimited:
		// The provider said "not now", which says nothing about this message.
		return update{status: statusRetrying, attempts: item.attempts, retryIn: result.RetryAfter, reason: outcome}, nil
	case outcomeRejected:
		return update{status: statusFailed, attempts: used, reason: outcome}, nil
	}
	if used < w.config.MaxAttempts {
		return update{status: statusRetrying, attempts: used, retryIn: w.config.Backoff(used), reason: outcome}, nil
	}
	ambiguous := outcome == outcomeTimeout
	if !ambiguous {
		// A call that got no answer may have gone through even if later ones failed.
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
		defer cancel()
		err := w.db.QueryRow(recordCtx, `SELECT EXISTS (SELECT 1 FROM delivery_attempts WHERE delivery_id = $1 AND outcome = $2)`, item.id, outcomeTimeout).Scan(&ambiguous)
		if err != nil {
			return update{}, fmt.Errorf("read earlier attempts: %w", err)
		}
	}
	if ambiguous {
		return update{status: statusUnknown, attempts: used, reason: outcome}, nil
	}
	return update{status: statusFailed, attempts: used, reason: outcome}, nil
}

// record stores an outcome under the claim. It runs even during shutdown, and a
// claim that has moved on changes nothing.
func (w *Worker) record(ctx context.Context, item claim, next update, attempt *attemptRow) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	result, err := tx.Exec(ctx, `
		UPDATE campaign_deliveries
		SET status = $3, attempts = $4, next_attempt_at = now() + make_interval(secs => $5),
		    last_error = NULLIF($6, ''), provider_message_id = NULLIF($7, ''),
		    sent_at = CASE WHEN $3 = 'SENT' THEN now() END,
		    claim_token = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $1 AND claim_token = $2 AND status = 'SENDING'`,
		item.id, item.token, next.status, next.attempts, next.retryIn.Seconds(), next.reason, next.messageID)
	if err != nil {
		return fmt.Errorf("update delivery: %w", err)
	}
	if result.RowsAffected() == 0 {
		slog.WarnContext(ctx, "claim was lost, outcome not recorded", "delivery_id", item.id)
		return nil
	}
	if attempt != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (delivery_id, campaign_id, attempt, response_code, outcome, error) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))`,
			item.id, item.campaignID, attempt.number, attempt.code, attempt.outcome, attempt.err); err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit record: %w", err)
	}
	return nil
}

// RecoverExpired takes back deliveries whose worker stopped without recording an
// outcome. That worker may or may not have reached the provider, so the unfinished
// call counts as an attempt without an answer. Retrying is safe because the
// idempotency key stays the same, and counting it stops a message that keeps
// crashing workers from doing so forever.
func (w *Worker) RecoverExpired(ctx context.Context) (int, error) {
	rows, err := w.db.Query(ctx, `
		WITH expired AS (
			SELECT id FROM campaign_deliveries
			WHERE status = 'SENDING' AND lease_expires_at < now()
			ORDER BY lease_expires_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		), recovered AS (
			UPDATE campaign_deliveries d
			SET status = CASE WHEN d.attempts + 1 >= $2 THEN 'UNKNOWN' ELSE 'RETRYING' END,
			    attempts = d.attempts + 1, next_attempt_at = now(), last_error = $3,
			    claim_token = NULL, lease_expires_at = NULL, updated_at = now()
			FROM expired
			WHERE d.id = expired.id
			RETURNING d.id, d.campaign_id, d.attempts, d.channel
		), recorded AS (
			INSERT INTO delivery_attempts (delivery_id, campaign_id, attempt, outcome, error)
			SELECT id, campaign_id, attempts, $4, 'worker stopped before recording an outcome' FROM recovered
		)
		SELECT channel, count(*) FROM recovered GROUP BY channel`,
		recoverBatch, w.config.MaxAttempts, reasonLeaseExpired, outcomeTimeout)
	if err != nil {
		return 0, fmt.Errorf("recover deliveries: %w", err)
	}
	total := 0
	for rows.Next() {
		var (
			channel string
			count   int
		)
		if err := rows.Scan(&channel, &count); err != nil {
			rows.Close()
			return total, fmt.Errorf("recover deliveries: %w", err)
		}
		w.metrics.DeliveryRecovered.WithLabelValues(channel).Add(float64(count))
		total += count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return total, fmt.Errorf("recover deliveries: %w", err)
	}
	if total > 0 {
		slog.WarnContext(ctx, "recovered deliveries from a stopped worker", "count", total)
	}
	return total, nil
}

// FinishCampaigns closes campaigns with nothing left to do. It runs after outcomes
// are committed and on a timer, because two workers finishing the last deliveries
// at once could each still see the other's as open.
func (w *Worker) FinishCampaigns(ctx context.Context) error {
	rows, err := w.db.Query(ctx, `
		SELECT c.id::text FROM campaigns c
		WHERE c.status = 'DISPATCHING' AND NOT EXISTS (
			SELECT 1 FROM campaign_deliveries d
			WHERE d.campaign_id = c.id AND d.status IN ('QUEUED', 'SENDING', 'RETRYING')
		)`)
	if err != nil {
		return fmt.Errorf("find finished campaigns: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("find finished campaigns: %w", err)
	}
	var failures []error
	for _, id := range ids {
		failures = append(failures, w.finishCampaign(ctx, id))
	}
	return errors.Join(failures...)
}

func (w *Worker) finishCampaign(ctx context.Context, id string) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finish: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	counts := map[string]int{}
	rows, err := tx.Query(ctx, `SELECT status, count(*) FROM campaign_deliveries WHERE campaign_id = $1 GROUP BY status`, id)
	if err != nil {
		return fmt.Errorf("count deliveries: %w", err)
	}
	for rows.Next() {
		var (
			status string
			count  int
		)
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return fmt.Errorf("count deliveries: %w", err)
		}
		counts[status] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("count deliveries: %w", err)
	}

	var moreRuns bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaign_schedules WHERE campaign_id = $1 AND next_run_at IS NOT NULL)`, id).Scan(&moreRuns); err != nil {
		return fmt.Errorf("read schedule: %w", err)
	}
	status, event := "COMPLETED", "campaign.completed"
	switch {
	case moreRuns:
		status, event = "SCHEDULED", "campaign.run_finished"
	case counts[statusFailed]+counts[statusUnknown] > 0:
		status = "COMPLETED_WITH_FAILURES"
	}
	result, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1 AND status = 'DISPATCHING'`, id, status)
	if err != nil {
		return fmt.Errorf("finish campaign: %w", err)
	}
	if result.RowsAffected() == 0 {
		return nil // another worker got here first
	}
	details, err := json.Marshal(map[string]any{
		"accepted": counts[statusSent], "failed": counts[statusFailed], "unknown": counts[statusUnknown], "skipped": counts[statusSkipped],
	})
	if err != nil {
		return fmt.Errorf("encode audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (campaign_id, event_type, details) VALUES ($1, $2, $3)`, id, event, details); err != nil {
		return fmt.Errorf("record completion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finish: %w", err)
	}
	return nil
}

func (w *Worker) reportQueueDepth(ctx context.Context) error {
	rows, err := w.db.Query(ctx, `SELECT channel, count(*) FROM campaign_deliveries WHERE status IN ('QUEUED', 'RETRYING') GROUP BY channel`)
	if err != nil {
		return fmt.Errorf("read queue depth: %w", err)
	}
	depth := map[string]float64{"sms": 0, "email": 0}
	for rows.Next() {
		var (
			channel string
			count   float64
		)
		if err := rows.Scan(&channel, &count); err != nil {
			rows.Close()
			return fmt.Errorf("read queue depth: %w", err)
		}
		depth[channel] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read queue depth: %w", err)
	}
	for channel, count := range depth {
		w.metrics.DeliveryQueueDepth.WithLabelValues(channel).Set(count)
	}
	return nil
}
