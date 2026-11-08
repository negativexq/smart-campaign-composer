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
	"strconv"
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
	// outcomeUnauthorized is a 401: the provider refused the channel's credentials,
	// which says nothing about the message and holds the whole channel.
	outcomeUnauthorized = "unauthorized"
	// outcomeTimeout covers every call that ended without a response: the
	// provider may have the message, so it can never be called a plain failure.
	outcomeTimeout = "timeout"

	reasonLeaseExpired = "lease_expired"
	reasonNotSent      = "provider_unreachable"
	// The reasons a "not messaged recently" rule holds a message back before sending.
	reasonMessagedRecently = "messaged_recently"
	reasonAnotherSending   = "another_message_sending"
	reasonContactCheck     = "contact_check_failed"
	// sendingWait is how long a message waits for another one to the same person that
	// is being sent: about as long as that call may take.
	sendingWait = 5 * time.Second
	// overlapMargin widens the time since which another run counts as overlapping,
	// to cover clocks that differ between the API, the workers and PostgreSQL.
	overlapMargin = time.Minute

	recoverBatch       = 500
	notSentPause       = 5 * time.Second
	recordTimeout      = 5 * time.Second
	defaultSendTimeout = 5 * time.Second
	// answerMargin is how much longer than the send timeout a call counts as in flight
	// before it has an answer: the time between settling its permit and sending it.
	answerMargin      = 2 * time.Second
	finishInterval    = time.Second
	stallInterval     = 30 * time.Second
	defaultStallAfter = 10 * time.Minute
	idlePause         = 200 * time.Millisecond
	// claimGap lets a few slots free up before the next claim, so a busy worker
	// takes several deliveries per query instead of running one per finished call.
	claimGap   = 20 * time.Millisecond
	errorPause = 2 * time.Second

	// What a channel's loop can be doing, for campaign_delivery_loop_seconds_total.
	stateSlotWait   = "slot_wait"
	stateClaimGap   = "claim_gap"
	stateReserve    = "reserve"
	stateClaim      = "claim"
	stateSettle     = "settle"
	stateHoldPause  = "hold_pause"
	stateHoldWindow = "hold_window"
	stateHoldBreak  = "hold_breaker"
	stateIdle       = "idle"
	stateError      = "error"

	pauseSourceRateLimit   = "rate_limited"
	pauseSourceBreaker     = "breaker"
	pauseSourceNotSent     = "not_sent"
	pauseSourceCredentials = "credentials"
)

// channels is every channel the worker sends on.
var channels = []string{"sms", "email"}

// MaxDrain is the longest a worker can take to stop once told to: a claim already
// under way, then one provider call that gets no answer, pausing the channel and
// recording the outcome. Whatever runs the worker must wait longer than this, or
// messages stay in flight until their lease expires.
const MaxDrain = recordTimeout + defaultSendTimeout + recordTimeout + recordTimeout

// BreakerConfig says when a channel stops calling its provider.
type BreakerConfig struct {
	Window       time.Duration
	MinCalls     int
	FailureRatio float64
	FirstWait    time.Duration
	MaxWait      time.Duration
	Probes       int
}

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

// Permits is a limiter's answer. With none granted, Retry says when asking again
// can help, and Paused says the channel is resting after a provider's answer rather
// than at its limit.
type Permits struct {
	Granted int
	Retry   time.Duration
	Paused  bool
	// Probe is set on the one permit that tests a channel whose credentials were
	// refused: the generation of the lock it tests, which only it can open.
	Probe int64
	// id names this reservation, so settling it touches only its own permits.
	id string
}

// Limiter hands out permission to send on a channel.
type Limiter interface {
	// Reserve grants up to want sends. An error means there is no permission to send.
	Reserve(ctx context.Context, channel string, want int) (Permits, error)
	// Settle says how many of the permits are about to be used. The rest are given
	// back, and it answers how many of those used may really go out. Those count as
	// in flight for up to hold, until Answered says each one came back.
	Settle(ctx context.Context, channel string, permits Permits, used int, hold time.Duration) (int, error)
	// Answered says one call sent with permits came back at, or never left. A call
	// that got no answer is not reported: it may still reach the provider, so it
	// keeps counting for the whole hold.
	Answered(channel string, permits Permits, at time.Time)
	Pause(ctx context.Context, channel string, length time.Duration) error
	// LockCredentials holds the channel for every worker after a 401, apart from one
	// probe every interval. It says whether this call is what locked it.
	LockCredentials(ctx context.Context, channel string) (bool, error)
	// UnlockCredentials opens the channel after its probe got through, only if the
	// lock that probe tested is still in place. It says whether it opened it.
	UnlockCredentials(ctx context.Context, channel string, generation int64) (bool, error)
	// ReleaseProbe hands back a probe that sent nothing.
	ReleaseProbe(ctx context.Context, channel string, generation int64) error
	// CredentialsLocked says whether the channel is held after a 401.
	CredentialsLocked(ctx context.Context, channel string) (bool, error)
}

// ErrNotSent means the call never reached the provider.
var ErrNotSent = errors.New("message was not sent")

// Config tunes a worker.
type Config struct {
	BatchSize int
	// Concurrency is how many calls each channel makes at once.
	Concurrency int
	Lease       time.Duration
	MaxAttempts int
	SendTimeout time.Duration
	Backoff     func(attempt int) time.Duration
	// Breaker stops calling a provider that is failing. Without MinCalls it is off.
	Breaker BreakerConfig
	// StallAfter is how long a campaign may wait on retries with nothing finishing
	// before it stops and someone is told. Zero leaves it waiting.
	StallAfter time.Duration
	// AlertAfter is the span channel alerts look back over. Zero turns them off.
	AlertAfter time.Duration
	// AssetBase is the public address the images in emails load from.
	AssetBase string
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
		StallAfter:  defaultStallAfter,
		AlertAfter:  defaultAlertAfter,
		// Failures as usual stay far below half the calls, and twenty calls keep a
		// short bad patch from stopping a channel.
		Breaker: BreakerConfig{Window: 10 * time.Second, MinCalls: 20, FailureRatio: 0.5, FirstWait: 5 * time.Second, MaxWait: maxPause, Probes: 3},
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
	// breakers holds one per channel, as their providers fail separately.
	breakers map[string]*breaker
}

// New builds a worker.
func New(db *pgxpool.Pool, sender Sender, limiter Limiter, metrics *telemetry.Metrics, tracer trace.Tracer, config Config) *Worker {
	breakers := map[string]*breaker{}
	for _, channel := range channels {
		breakers[channel] = newBreaker(config.Breaker, time.Now)
	}
	return &Worker{db, sender, limiter, metrics, tracer, config, breakers}
}

// Run works until ctx ends, then lets calls in progress finish and be recorded.
func (w *Worker) Run(ctx context.Context) error {
	var housekeeping sync.WaitGroup
	housekeeping.Go(func() { w.runHousekeeping(ctx) })
	defer housekeeping.Wait()

	// Each channel has its own loop and slots, so one that is resting or slow costs
	// the other nothing.
	var channelLoops sync.WaitGroup
	for _, channel := range channels {
		channelLoops.Go(func() { w.runChannel(ctx, channel) })
	}
	channelLoops.Wait()
	return nil
}

// runChannel refills each slot as soon as its call ends. Waiting for a whole round
// instead would let one call with no answer stop the rest until it times out.
func (w *Worker) runChannel(ctx context.Context, channel string) {
	concurrency := max(w.config.Concurrency, 1)
	slots := make(chan struct{}, concurrency)
	for range concurrency {
		slots <- struct{}{}
	}
	var sending sync.WaitGroup
	defer sending.Wait()

	for ctx.Err() == nil {
		free := takeFreeSlots(ctx, slots, func(state string, since time.Time) { w.spent(channel, state, since) })
		if free == 0 {
			return
		}
		claims, pause, state, err := w.takeDue(ctx, channel, min(free, max(w.config.BatchSize, 1)))
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "process deliveries", "channel", channel, "error", err)
		}
		for range free - len(claims) {
			slots <- struct{}{}
		}
		for _, item := range claims {
			sending.Go(func() {
				defer func() { slots <- struct{}{} }()
				w.handleLogged(ctx, item)
			})
		}
		waited := time.Now()
		select {
		case <-ctx.Done():
		case <-time.After(pause):
		}
		if state != "" {
			w.spent(channel, state, waited)
			w.observeHold(ctx, channel, state, waited)
		}
	}
}

// spent adds the time since since to what the channel's loop spent in state.
func (w *Worker) spent(channel, state string, since time.Time) {
	w.metrics.DeliveryLoopSeconds.WithLabelValues(channel, state).Add(time.Since(since).Seconds())
}

// takeDue claims deliveries only after getting permission to send them. Claiming
// first would leave them waiting for the limiter, holding slots and running down
// their leases, until another worker could take them and send them a second time.
// The duration says how long to wait before trying again, and the state what that
// wait is for; it is empty when there is nothing to wait for.
func (w *Worker) takeDue(ctx context.Context, channel string, want int) ([]claim, time.Duration, string, error) {
	breaker := w.breakers[channel]
	allowed, retry, probing := breaker.allow(want)
	if allowed == 0 {
		return nil, retry, stateHoldBreak, nil
	}
	reserving := time.Now()
	permits, err := w.limiter.Reserve(ctx, channel, allowed)
	granted := time.Now()
	w.spent(channel, stateReserve, reserving)
	w.metrics.LimiterReserveDuration.WithLabelValues(channel).Observe(granted.Sub(reserving).Seconds())
	if err != nil {
		breaker.giveBack(allowed)
		return nil, errorPause, stateError, err
	}
	if permits.Granted == 0 {
		breaker.giveBack(allowed)
		w.metrics.DeliveryRateLimitWait.WithLabelValues(channel).Observe(permits.Retry.Seconds())
		if permits.Paused {
			return nil, permits.Retry, stateHoldPause, nil
		}
		return nil, permits.Retry, stateHoldWindow, nil
	}
	w.metrics.LimiterGranted.WithLabelValues(channel).Add(float64(permits.Granted))
	claims, timing, err := w.claim(ctx, channel, permits.Granted)
	claimed := time.Now()
	w.spent(channel, stateClaim, granted)
	w.metrics.LimiterUnused.WithLabelValues(channel).Add(float64(permits.Granted - len(claims)))
	claims = w.settle(ctx, channel, permits, claims)
	settled := time.Now()
	w.spent(channel, stateSettle, claimed)
	w.metrics.LimiterSettleDuration.WithLabelValues(channel).Observe(settled.Sub(claimed).Seconds())
	breaker.giveBack(allowed - len(claims))
	if permits.Probe != 0 && len(claims) == 0 {
		w.releaseProbe(ctx, channel, permits.Probe)
	}
	var link trace.SpanContext
	if len(claims) > 0 {
		link = w.traceTake(ctx, channel, len(claims), reserving, granted, settled, claimed, timing)
	}
	for i := range claims {
		claims[i].probe = probing
		claims[i].granted = granted
		claims[i].take = link
		claims[i].permits = permits
	}
	switch {
	case err != nil:
		return nil, errorPause, stateError, err
	case len(claims) == 0:
		return nil, idlePause, stateIdle, nil
	}
	return claims, 0, "", nil
}

// traceTake writes one round's spans once it is known that the round found work: an
// idle worker polls several times a second and its rounds are not worth keeping. The
// messages sent from this round link to the span it returns.
func (w *Worker) traceTake(ctx context.Context, channel string, claimed int, reserving, granted, settled, claimEnd time.Time, timing claimTiming) trace.SpanContext {
	ctx, span := w.tracer.Start(ctx, "delivery.take", trace.WithTimestamp(reserving), trace.WithAttributes(
		attribute.String("delivery.channel", channel),
		attribute.Int("delivery.claimed", claimed),
	))
	mark := func(ctx context.Context, name string, from, to time.Time, attributes ...attribute.KeyValue) context.Context {
		ctx, child := w.tracer.Start(ctx, name, trace.WithTimestamp(from), trace.WithAttributes(attributes...))
		child.End(trace.WithTimestamp(to))
		return ctx
	}
	mark(ctx, "limiter.reserve", reserving, granted)
	claimCtx := mark(ctx, "delivery.claim", granted, claimEnd, attribute.Int("delivery.claimed", claimed), attribute.Float64("delivery.decode_ms", float64(timing.decode.Microseconds())/1000))
	mark(claimCtx, "db.acquire", granted, timing.acquired)
	mark(claimCtx, "db.execute", timing.acquired, timing.executed)
	mark(claimCtx, "db.read", timing.executed, claimEnd)
	mark(ctx, "limiter.settle", claimEnd, settled)
	span.End(trace.WithTimestamp(settled))
	return span.SpanContext()
}

// settle gives back what was not claimed, which keeps an idle queue from using up the
// channel's rate, and counts the rest from now. A claim that outlasted its permits may
// find the window full: those messages go back in the queue, at no cost to them. So do
// all of them if settling fails, because this can take seconds and the limiter may be
// gone by then, and nothing is sent without it.
func (w *Worker) settle(ctx context.Context, channel string, permits Permits, claims []claim) []claim {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	confirmed, err := w.limiter.Settle(settleCtx, channel, permits, len(claims), w.config.SendTimeout+answerMargin)
	if err != nil {
		slog.WarnContext(ctx, "settle permits", "channel", channel, "error", err)
		confirmed = 0
	}
	for _, item := range claims[confirmed:] {
		next := update{status: statusRetrying, attempts: item.attempts, retryIn: time.Second}
		if err := w.record(ctx, item, next, nil); err != nil {
			slog.ErrorContext(ctx, "return claim", "delivery_id", item.id, "error", err)
		}
	}
	return claims[:confirmed]
}

// observeHold records a wait that has just ended, at its real length.
func (w *Worker) observeHold(ctx context.Context, channel, state string, waited time.Time) {
	var reason string
	switch state {
	case stateHoldPause:
		reason = "pause"
	case stateHoldWindow:
		reason = "window"
	case stateHoldBreak:
		reason = "breaker"
	default:
		return
	}
	w.metrics.LimiterActualWait.WithLabelValues(channel, reason).Observe(time.Since(waited).Seconds())
	_, span := w.tracer.Start(ctx, "delivery.limit", trace.WithTimestamp(waited), trace.WithAttributes(
		attribute.String("delivery.channel", channel),
		attribute.String("delivery.limit.reason", reason),
	))
	span.End()
}

// takeFreeSlots waits for a free slot, gives others a moment to free up and takes
// them all. It returns 0 only when ctx ends.
func takeFreeSlots(ctx context.Context, slots chan struct{}, spent func(state string, since time.Time)) int {
	waiting := time.Now()
	select {
	case <-ctx.Done():
		return 0
	case <-slots:
	}
	spent(stateSlotWait, waiting)
	free := 1
	gap := time.Now()
	select {
	case <-ctx.Done():
	case <-time.After(claimGap):
	}
	spent(stateClaimGap, gap)
	for {
		select {
		case <-slots:
			free++
		default:
			return free
		}
	}
}

func (w *Worker) runHousekeeping(ctx context.Context) {
	ticker := time.NewTicker(finishInterval)
	defer ticker.Stop()
	stalls := time.NewTicker(stallInterval)
	defer stalls.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stalls.C:
			if err := w.PauseStalled(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "pause stalled campaigns", "error", err)
			}
			if err := w.CheckAlerts(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "check channel alerts", "error", err)
			}
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
	id, token, campaignID, runID, channel, key string
	userID                                     int64
	attempts                                   int
	traceContext                               *string
	firstName, email, phone                    string
	consented                                  bool
	// notMessagedWithinDays is the campaign's "not messaged recently" rule, if it has one.
	notMessagedWithinDays *int
	// overlapping says another run has been open since this run's queuing counted
	// recent messages, so one of its messages may have reached the person since.
	// Only then does a message with the rule check again before sending.
	overlapping bool
	// ruleOpen says another run with the rule is open, so a message without the
	// rule must not slip in while one of its messages to the person is being sent.
	ruleOpen bool
	// probe marks a call testing whether a failing provider is back.
	probe   bool
	content content.Content
	// granted is when the limiter gave this message's permit, and take the round that
	// claimed it, which is in another trace.
	granted time.Time
	take    trace.SpanContext
	// permits is the reservation this message's permit came from.
	permits Permits
}

// claimTiming is when a claim passed each stage, and the part of it spent decoding.
type claimTiming struct {
	acquired, executed time.Time
	decode             time.Duration
}

// ProcessBatch takes one batch per channel and waits for all of it, returning how
// many it claimed. Run does not work this way; this is for stepping through a queue.
func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	var (
		claims []claim
		failed error
	)
	for _, channel := range channels {
		taken, _, _, err := w.takeDue(ctx, channel, w.config.BatchSize)
		claims = append(claims, taken...)
		failed = errors.Join(failed, err)
	}
	var (
		wg    sync.WaitGroup
		slots = make(chan struct{}, max(w.config.Concurrency, 1))
	)
	for _, item := range claims {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			w.handleLogged(ctx, item)
		})
	}
	wg.Wait()
	return len(claims), failed
}

func (w *Worker) handleLogged(ctx context.Context, item claim) {
	if err := w.handle(ctx, item); err != nil {
		slog.ErrorContext(ctx, "record delivery", "delivery_id", item.id, "error", err)
	}
}

// claim outlives a shutdown: giving up on a claim the database has already
// committed would strand those rows as SENDING until their lease expires.
func (w *Worker) claim(ctx context.Context, channel string, limit int) ([]claim, claimTiming, error) {
	started := time.Now()
	var timing claimTiming
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	conn, err := w.db.Acquire(ctx)
	if err != nil {
		return nil, timing, fmt.Errorf("claim deliveries: %w", err)
	}
	defer conn.Release()
	timing.acquired = time.Now()
	rows, err := conn.Query(ctx, `
		WITH next AS (
			SELECT d.id
			FROM campaign_deliveries d
			-- A subquery rather than a join: with one campaign sending, a join makes the
			-- planner fetch and sort its whole queue to take a few rows, which took 700 ms
			-- with a million waiting. This way it reads the claimable index in order.
			WHERE d.channel = $3 AND d.status IN ('QUEUED', 'RETRYING') AND d.next_attempt_at <= now()
			  AND (SELECT c.status FROM campaigns c WHERE c.id = d.campaign_id) = 'DISPATCHING'
			ORDER BY d.next_attempt_at
			LIMIT $1
			FOR UPDATE OF d SKIP LOCKED
		)
		UPDATE campaign_deliveries d
		SET status = 'SENDING', claim_token = gen_random_uuid(), lease_expires_at = now() + make_interval(secs => $2), updated_at = now()
		FROM next, users u, campaign_versions v, campaign_runs r
		WHERE d.id = next.id AND u.id = d.user_id AND v.id = d.campaign_version_id AND r.id = d.run_id
		RETURNING d.id::text, d.claim_token::text, d.campaign_id::text, d.run_id::text, d.channel, d.idempotency_key, d.user_id, d.attempts, d.trace_context,
		          GREATEST(EXTRACT(EPOCH FROM now() - d.next_attempt_at), 0)::float8,
		          u.first_name, u.email, u.phone, u.language, COALESCE(d.variant, ''),
		          COALESCE((SELECT allowed FROM consents WHERE user_id = d.user_id AND channel = d.channel), false),
		          v.content, (v.segment #>> '{segment,not_messaged_within_days}')::int,
		          r.rule_days IS NOT NULL AND EXISTS (
		              SELECT 1 FROM campaign_runs o
		              WHERE o.id <> r.id AND (o.finished_at IS NULL OR o.finished_at >= COALESCE(r.rule_as_of, '-infinity') - make_interval(secs => $4))),
		          r.rule_days IS NULL AND EXISTS (
		              SELECT 1 FROM campaign_runs o
		              WHERE o.id <> r.id AND o.finished_at IS NULL AND o.rule_days IS NOT NULL)`,
		limit, w.config.Lease.Seconds(), channel, overlapMargin.Seconds())
	if err != nil {
		return nil, timing, fmt.Errorf("claim deliveries: %w", err)
	}
	timing.executed = time.Now()
	queueAge := w.metrics.DeliveryQueueAge.WithLabelValues(channel)
	claims, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (claim, error) {
		decoding := time.Now()
		defer func() { timing.decode += time.Since(decoding) }()
		var (
			item     claim
			encoded  []byte
			language string
			variant  string
			age      float64
		)
		if err := row.Scan(&item.id, &item.token, &item.campaignID, &item.runID, &item.channel, &item.key, &item.userID, &item.attempts, &item.traceContext,
			&age, &item.firstName, &item.email, &item.phone, &language, &variant, &item.consented, &encoded, &item.notMessagedWithinDays,
			&item.overlapping, &item.ruleOpen); err != nil {
			return claim{}, err
		}
		var stored content.Versions
		if err := json.Unmarshal(encoded, &stored); err != nil {
			return claim{}, fmt.Errorf("decode approved content: %w", err)
		}
		item.content = stored.ForPerson(variant, language)
		queueAge.Observe(age)
		return item, nil
	})
	if err != nil {
		return nil, timing, fmt.Errorf("claim deliveries: %w", err)
	}
	// Only claims that found work: an idle worker polls several times a second.
	if len(claims) > 0 {
		finished := time.Now()
		stages := w.metrics.DeliveryClaimDuration
		stages.WithLabelValues(channel, "acquire").Observe(timing.acquired.Sub(started).Seconds())
		stages.WithLabelValues(channel, "execute").Observe(timing.executed.Sub(timing.acquired).Seconds())
		stages.WithLabelValues(channel, "read").Observe((finished.Sub(timing.executed) - timing.decode).Seconds())
		stages.WithLabelValues(channel, "decode").Observe(timing.decode.Seconds())
		stages.WithLabelValues(channel, "total").Observe(finished.Sub(started).Seconds())
	}
	return claims, timing, nil
}

func (c claim) message(unsubscribeURL, assetBase string) (Message, error) {
	message := Message{Channel: c.channel, IdempotencyKey: c.key}
	if c.channel == "sms" {
		message.To = c.phone
		message.Body = content.Render(c.content.SMS, content.NameFor(c.content.Language, c.firstName), c.content.DiscountCode)
		return message, nil
	}
	message.To = c.email
	message.UnsubscribeURL = unsubscribeURL
	email, err := content.RenderEmailWith(c.content, c.firstName, unsubscribeURL, content.RenderOptions{AssetBase: assetBase})
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
		w.limiter.Answered(item.channel, item.permits, time.Now())
		w.breakers[item.channel].giveBack(1)
		span.SetAttributes(attribute.String("delivery.outcome", "consent_withdrawn"))
		return w.record(ctx, item, update{status: statusSkipped, attempts: item.attempts, reason: "consent_withdrawn"}, nil)
	}
	if (item.notMessagedWithinDays != nil && item.overlapping) || (item.notMessagedWithinDays == nil && item.ruleOpen) {
		held, err := w.holdBack(ctx, item)
		if err != nil {
			slog.ErrorContext(ctx, "check recent messages", "delivery_id", item.id, "error", err)
			w.limiter.Answered(item.channel, item.permits, time.Now())
			w.breakers[item.channel].giveBack(1)
			span.SetAttributes(attribute.String("delivery.outcome", reasonContactCheck))
			return w.record(ctx, item, update{status: statusRetrying, attempts: item.attempts, retryIn: notSentPause, reason: reasonContactCheck}, nil)
		}
		if held {
			w.limiter.Answered(item.channel, item.permits, time.Now())
			w.breakers[item.channel].giveBack(1)
			span.SetAttributes(attribute.String("delivery.outcome", "held_back"))
			return nil
		}
	}

	message, err := item.message(w.unsubscribeURL(item), w.config.AssetBase)
	if err != nil {
		w.limiter.Answered(item.channel, item.permits, time.Now())
		w.breakers[item.channel].giveBack(1)
		slog.ErrorContext(ctx, "render message", "delivery_id", item.id, "error", err)
		return w.recordNotSent(ctx, span, item)
	}

	result, sendErr := w.send(ctx, item, message)
	if errors.Is(sendErr, ErrNotSent) {
		w.noteBreaker(ctx, item.channel, true)
		return w.recordNotSent(ctx, span, item)
	}
	outcome, code := classify(result, sendErr)
	span.SetAttributes(attribute.String("delivery.outcome", outcome))
	w.metrics.DeliveryAttempts.WithLabelValues(item.channel, outcome).Inc()
	switch outcome {
	case outcomeRateLimited, outcomeUnauthorized:
		// "Not now" and "not you" say nothing about whether the provider is healthy.
		// A probe given back here is not left waiting for a result that never comes.
		w.breakers[item.channel].giveBack(1)
	default:
		w.noteBreaker(ctx, item.channel, outcome == outcomeServerError || outcome == outcomeTimeout)
	}
	if result.Duplicate {
		w.metrics.DeliveryDuplicates.WithLabelValues(item.channel).Inc()
	}
	if outcome != outcomeAccepted {
		span.SetStatus(codes.Error, outcome)
	}

	switch {
	case outcome == outcomeUnauthorized:
		w.lockCredentials(ctx, item)
	case outcome == outcomeAccepted && item.permits.Probe != 0:
		w.unlockCredentials(ctx, item)
	}

	next, err := w.nextStep(ctx, item, outcome, result)
	if err != nil {
		return err
	}
	if next.status == statusRetrying {
		w.metrics.DeliveryRetries.WithLabelValues(item.channel, outcome).Inc()
	}
	if outcome == outcomeRateLimited {
		w.pause(ctx, item.channel, result.RetryAfter, pauseSourceRateLimit)
	}
	attemptError := ""
	if sendErr != nil {
		attemptError = "no response from the provider"
	}
	number := item.attempts + 1
	return w.record(ctx, item, next, &attemptRow{number, code, outcome, attemptError})
}

// noteBreaker gives the breaker a call's result and acts on what it decides.
func (w *Worker) noteBreaker(ctx context.Context, channel string, failed bool) {
	hold, closed := w.breakers[channel].record(failed)
	switch {
	case hold > 0:
		w.metrics.DeliveryCircuitOpen.WithLabelValues(channel).Set(1)
		w.metrics.DeliveryCircuitTrips.WithLabelValues(channel).Inc()
		slog.WarnContext(ctx, "provider is failing, holding the channel", "channel", channel, "hold", hold)
		// This tells other workers; this one is held by its breaker either way.
		w.pause(ctx, channel, hold, pauseSourceBreaker)
	case closed:
		w.metrics.DeliveryCircuitOpen.WithLabelValues(channel).Set(0)
		slog.InfoContext(ctx, "provider is back, channel resumes", "channel", channel)
	}
}

// lockCredentials holds the channel for every worker after a 401: an expired or
// revoked key will not fix itself in a few seconds, and every call in the meantime
// would be refused. The worker that locks it raises the alert at once rather than at
// the next check, because a healthy channel in the same campaign keeps it moving.
func (w *Worker) lockCredentials(ctx context.Context, item claim) {
	lockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	locked, err := w.limiter.LockCredentials(lockCtx, item.channel)
	if err != nil {
		// Without Redis nothing is granted anyway; a pause is the best left to do.
		slog.ErrorContext(ctx, "lock channel after refused credentials", "channel", item.channel, "error", err)
		w.pause(ctx, item.channel, maxPause, pauseSourceCredentials)
		return
	}
	if !locked {
		return
	}
	w.metrics.DeliveryCredentialLocks.WithLabelValues(item.channel).Inc()
	slog.ErrorContext(ctx, "provider refused the channel's credentials, holding the channel", "channel", item.channel)
	if err := w.checkAlerts(lockCtx, []string{item.channel}); err != nil {
		slog.ErrorContext(ctx, "raise alert for refused credentials", "channel", item.channel, "error", err)
	}
}

// unlockCredentials opens the channel after its probe got through. The message the
// probe carried is recorded as sent like any other.
func (w *Worker) unlockCredentials(ctx context.Context, item claim) {
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	opened, err := w.limiter.UnlockCredentials(unlockCtx, item.channel, item.permits.Probe)
	if err != nil {
		// The next probe tries again.
		slog.ErrorContext(ctx, "open channel after accepted probe", "channel", item.channel, "error", err)
		return
	}
	if !opened {
		return
	}
	slog.InfoContext(ctx, "provider accepted the channel's credentials again, channel resumes", "channel", item.channel)
	if err := w.checkAlerts(unlockCtx, []string{item.channel}); err != nil {
		slog.ErrorContext(ctx, "close alert for refused credentials", "channel", item.channel, "error", err)
	}
}

func (w *Worker) releaseProbe(ctx context.Context, channel string, generation int64) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if err := w.limiter.ReleaseProbe(releaseCtx, channel, generation); err != nil {
		slog.WarnContext(ctx, "release unused probe", "channel", channel, "error", err)
	}
}

// recordNotSent is for when nothing went out for certain, so it costs no attempt
// and the campaign can wait out a provider outage instead of using up its retries.
func (w *Worker) recordNotSent(ctx context.Context, span trace.Span, item claim) error {
	span.SetAttributes(attribute.String("delivery.outcome", reasonNotSent))
	w.metrics.DeliveryAttempts.WithLabelValues(item.channel, reasonNotSent).Inc()
	w.pause(ctx, item.channel, notSentPause, pauseSourceNotSent)
	return w.record(ctx, item, update{status: statusRetrying, attempts: item.attempts, retryIn: notSentPause, reason: reasonNotSent}, nil)
}

// pause tells every worker to hold a channel. Failing to do so only means more
// calls get a 429, so it must not stop the outcome from being recorded.
func (w *Worker) pause(ctx context.Context, channel string, length time.Duration, source string) {
	w.metrics.LimiterPause.WithLabelValues(channel, source).Observe(length.Seconds())
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
		attribute.Bool("delivery.probe", item.probe),
	)}
	if item.take.IsValid() {
		options = append(options, trace.WithLinks(trace.Link{SpanContext: item.take}))
	}
	if item.traceContext != nil {
		parent := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{"traceparent": *item.traceContext})
		options = append(options, trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(parent)}))
	}
	return w.tracer.Start(ctx, "delivery.send", options...)
}

// send outlives a shutdown: stopping halfway would leave the outcome unknown.
func (w *Worker) send(ctx context.Context, item claim, message Message) (Result, error) {
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.config.SendTimeout)
	defer cancel()
	callCtx, span := w.tracer.Start(callCtx, "delivery.provider")
	defer span.End()

	if !item.granted.IsZero() {
		w.metrics.DeliveryGrantToSend.WithLabelValues(item.channel).Observe(time.Since(item.granted).Seconds())
	}
	inflight := w.metrics.DeliveryInflight.WithLabelValues(item.channel)
	inflight.Inc()
	defer inflight.Dec()
	start := time.Now()
	result, err := w.sender.Send(callCtx, message)
	if err == nil || errors.Is(err, ErrNotSent) {
		// An answer means the provider has the call; no call left means none to count.
		w.limiter.Answered(item.channel, item.permits, time.Now())
	}
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
	case code == http.StatusUnauthorized:
		return outcomeUnauthorized, &code
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
	case outcomeUnauthorized:
		// The credentials were refused, not the message: it waits for the channel.
		return update{status: statusRetrying, attempts: item.attempts, retryIn: notSentPause, reason: outcome}, nil
	}
	if item.probe {
		// A failed probe says the provider is still down, nothing about this message.
		return update{status: statusRetrying, attempts: item.attempts, retryIn: w.config.Backoff(used), reason: outcome}, nil
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
	started := time.Now()
	defer func() {
		w.metrics.DeliveryRecordDuration.WithLabelValues(item.channel).Observe(time.Since(started).Seconds())
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	ctx, span := w.tracer.Start(ctx, "delivery.record")
	defer span.End()
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
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (delivery_id, campaign_id, run_id, channel, attempt, response_code, outcome, error) VALUES ($1, $2, $8, $3, $4, $5, $6, NULLIF($7, ''))`,
			item.id, item.campaignID, item.channel, attempt.number, attempt.code, attempt.outcome, attempt.err, item.runID); err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit record: %w", err)
	}
	return nil
}

// lockContact waits for the person's turn and says how long that took. The lock
// lasts until the end of the transaction, which in a batch outside one is the batch.
const lockContact = `
	SELECT EXTRACT(EPOCH FROM clock_timestamp() - statement_timestamp())::float8
	FROM (SELECT pg_advisory_xact_lock(hashtextextended($1, 0))) turn`

// checkContact finds what stands between a person and one more message under a "not
// messaged recently" rule, and holds the message back in the same statement: skipped
// as "messaged_recently" for a message from another send since the cutoff, counting
// one that may have arrived, or waiting as "another_message_sending" for one on its
// way now. A send that timed out may have got through, so a retry waiting after one
// counts from when that call was made. The message's own run is left out: its other
// channel is part of the same send. A message whose own earlier call got no answer is
// never skipped: the provider may have it, and only a retry with the same key tells,
// so it may wait but then goes. Nothing changes when its claim has moved on. It
// gives what it found and whether it wrote. It must run after lockContact, as a
// statement of its own, so that it sees what the last holder of the turn wrote.
const checkContact = `
	WITH seen AS (
		SELECT CASE
			WHEN NOT EXISTS (SELECT 1 FROM delivery_attempts mine WHERE mine.delivery_id = $5 AND mine.outcome = $4)
			 AND (EXISTS (
				SELECT 1 FROM campaign_deliveries d
				WHERE d.user_id = $1 AND d.run_id <> $2::uuid AND d.status IN ('SENT', 'UNKNOWN') AND d.updated_at >= $3)
			  OR EXISTS (
				SELECT 1 FROM campaign_deliveries d
				WHERE d.user_id = $1 AND d.run_id <> $2::uuid AND d.status = 'RETRYING'
				  AND EXISTS (SELECT 1 FROM delivery_attempts a WHERE a.delivery_id = d.id AND a.outcome = $4 AND a.occurred_at >= $3)))
			THEN 'messaged'
			WHEN EXISTS (
				SELECT 1 FROM campaign_deliveries d
				WHERE d.user_id = $1 AND d.run_id <> $2::uuid AND d.status = 'SENDING')
			THEN 'sending'
			ELSE ''
		END AS found
	), held AS (
		UPDATE campaign_deliveries d
		SET status = CASE WHEN seen.found = 'messaged' THEN 'SKIPPED' ELSE 'RETRYING' END,
		    last_error = CASE WHEN seen.found = 'messaged' THEN $7 ELSE $8 END,
		    next_attempt_at = now() + CASE WHEN seen.found = 'messaged' THEN interval '0' ELSE make_interval(secs => $9) END,
		    provider_message_id = NULL, sent_at = NULL, claim_token = NULL, lease_expires_at = NULL, updated_at = now()
		FROM seen
		WHERE seen.found <> '' AND d.id = $5 AND d.claim_token = $6 AND d.status = 'SENDING'
		RETURNING d.id
	)
	SELECT found, EXISTS (SELECT 1 FROM held) FROM seen`

// waitForRule holds back a message without a "not messaged recently" rule while
// another run's message with the rule to the same person is being sent, in the same
// statement, so that one is not sent after this one. It never skips: this campaign
// asked for no rule. It gives what it found and whether it wrote, like checkContact,
// and must also run after lockContact as a statement of its own.
const waitForRule = `
	WITH seen AS (
		SELECT CASE WHEN EXISTS (
			SELECT 1 FROM campaign_deliveries d JOIN campaign_runs r ON r.id = d.run_id
			WHERE d.user_id = $1 AND d.run_id <> $2::uuid AND d.status = 'SENDING' AND r.rule_days IS NOT NULL)
		THEN 'sending' ELSE '' END AS found
	), held AS (
		UPDATE campaign_deliveries d
		SET status = 'RETRYING', last_error = $5, next_attempt_at = now() + make_interval(secs => $6),
		    provider_message_id = NULL, sent_at = NULL, claim_token = NULL, lease_expires_at = NULL, updated_at = now()
		FROM seen
		WHERE seen.found <> '' AND d.id = $3 AND d.claim_token = $4 AND d.status = 'SENDING'
		RETURNING d.id
	)
	SELECT found, EXISTS (SELECT 1 FROM held) FROM seen`

// holdBack keeps a message with a "not messaged recently" rule from going out after
// another send has reached the person, or while one is on its way. Checks for one
// person take turns under a lock and write their outcome before letting go, so of
// two messages claimed together the first to look waits and the second, finding it
// no longer sending, goes. Both statements go in one batch, one round trip: outside a
// transaction PostgreSQL runs a batch as one implicit transaction, so the lock holds
// until the batch ends, while each statement still takes its own snapshot. The lock
// is not held while calling the provider: a message from a campaign without the
// rule waits instead, while a message with the rule to the same person is being sent
// (waitForRule). A message with the rule checks only when another run has been open
// since its queuing counted, and one without only when a run with the rule is open,
// so a campaign sending on its own does no extra work. It reports true when the
// message must not be sent now, including when its claim was lost.
func (w *Worker) holdBack(ctx context.Context, item claim) (held bool, err error) {
	started := time.Now()
	found := "error"
	defer func() {
		w.metrics.DeliveryContactCheck.WithLabelValues(item.channel, found).Observe(time.Since(started).Seconds())
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	ctx, span := w.tracer.Start(ctx, "delivery.contact_check")
	defer span.End()

	batch := &pgx.Batch{}
	batch.Queue(lockContact, contactLock(item.userID))
	if item.notMessagedWithinDays != nil {
		cutoff := time.Now().AddDate(0, 0, -*item.notMessagedWithinDays)
		batch.Queue(checkContact, item.userID, item.runID, cutoff, outcomeTimeout, item.id, item.token,
			reasonMessagedRecently, reasonAnotherSending, sendingWait.Seconds())
	} else {
		batch.Queue(waitForRule, item.userID, item.runID, item.id, item.token, reasonAnotherSending, sendingWait.Seconds())
	}
	results := w.db.SendBatch(ctx, batch)
	defer results.Close() //nolint:errcheck // closed below; this covers the early returns
	var waited float64
	if err := results.QueryRow().Scan(&waited); err != nil {
		return false, fmt.Errorf("lock contact: %w", err)
	}
	var (
		seen  string
		wrote bool
	)
	if err := results.QueryRow().Scan(&seen, &wrote); err != nil {
		return false, fmt.Errorf("check recent messages: %w", err)
	}
	// Closing reads to the end of the batch, after its implicit commit.
	if err := results.Close(); err != nil {
		return false, fmt.Errorf("commit contact check: %w", err)
	}
	w.metrics.DeliveryContactLockWait.WithLabelValues(item.channel).Observe(waited)
	switch seen {
	case "":
		found = "nothing"
		return false, nil
	case "messaged":
		found = reasonMessagedRecently
	default:
		found = reasonAnotherSending
	}
	span.SetAttributes(attribute.String("delivery.held_back", found))
	if !wrote {
		slog.WarnContext(ctx, "claim was lost, outcome not recorded", "delivery_id", item.id)
	}
	return true, nil
}

// contactLock is the advisory lock key for checks on one person.
func contactLock(userID int64) string {
	return "contact:" + strconv.FormatInt(userID, 10)
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
			RETURNING d.id, d.campaign_id, d.run_id, d.attempts, d.channel
		), recorded AS (
			INSERT INTO delivery_attempts (delivery_id, campaign_id, run_id, channel, attempt, outcome, error)
			SELECT id, campaign_id, run_id, channel, attempts, $4, 'worker stopped before recording an outcome' FROM recovered
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

// finishRuns keeps the counts of every run in where (a condition on r) that has
// nothing left to send. A run gets all its deliveries when it opens and a finished
// delivery does not change, so these counts are final. A run finishing in another
// transaction at the same time is passed over once that one commits.
const finishRuns = `
	UPDATE campaign_runs r
	SET finished_at = now(),
	    total = (SELECT count(*) FROM campaign_deliveries d WHERE d.run_id = r.id),
	    counts = COALESCE((
		SELECT jsonb_agg(jsonb_build_array(s.channel, s.status, s.deliveries, s.attempts) ORDER BY s.channel, s.status)
		FROM (SELECT channel, status, count(*) AS deliveries, sum(attempts) AS attempts
		      FROM campaign_deliveries WHERE run_id = r.id GROUP BY channel, status) s), '[]'),
	    responses = COALESCE((
		SELECT jsonb_object_agg(s.code, s.calls)
		FROM (SELECT response_code::text AS code, count(*) AS calls
		      FROM delivery_attempts WHERE run_id = r.id AND response_code IS NOT NULL GROUP BY response_code) s), '{}')
	WHERE r.finished_at IS NULL AND NOT EXISTS (
		SELECT 1 FROM campaign_deliveries d WHERE d.run_id = r.id AND d.status IN ('QUEUED', 'SENDING', 'RETRYING')
	)`

// FinishCampaigns closes runs and campaigns with nothing left to do. It runs after
// outcomes are committed and on a timer, because two workers finishing the last
// deliveries at once could each still see the other's as open.
func (w *Worker) FinishCampaigns(ctx context.Context) error {
	// A repeating campaign's run can finish while a later one is sending.
	if _, err := w.db.Exec(ctx, finishRuns); err != nil {
		return fmt.Errorf("finish runs: %w", err)
	}
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

	// A scheduled run adds deliveries under this row's lock, and nothing claims the
	// deliveries of a campaign that stopped sending. So the check comes after the lock,
	// in its own statement: one statement would keep the view from before the wait.
	var current string
	if err := tx.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1 FOR UPDATE`, id).Scan(&current); err != nil {
		return fmt.Errorf("lock campaign: %w", err)
	}
	if current != "DISPATCHING" {
		return nil // another worker got here first
	}
	var open bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM campaign_deliveries WHERE campaign_id = $1 AND status IN ('QUEUED', 'SENDING', 'RETRYING'))`, id).Scan(&open); err != nil {
		return fmt.Errorf("check open deliveries: %w", err)
	}
	if open {
		return nil
	}

	if _, err := tx.Exec(ctx, finishRuns+` AND r.campaign_id = $1`, id); err != nil {
		return fmt.Errorf("finish runs: %w", err)
	}
	counts := map[string]int{}
	rows, err := tx.Query(ctx, `
		SELECT c->>1, sum((c->>2)::int)
		FROM campaign_runs r, jsonb_array_elements(r.counts) c
		WHERE r.campaign_id = $1
		GROUP BY 1`, id)
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
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, id, status); err != nil {
		return fmt.Errorf("finish campaign: %w", err)
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

// stalledCondition is a campaign waiting on retries with nothing finishing for $1
// seconds. Messages that are only queued do not count: they may simply be behind
// another campaign's. Time starts over when a campaign starts or resumes.
const stalledCondition = `
	c.status = 'DISPATCHING' AND c.status_since < now() - make_interval(secs => $1)
	AND EXISTS (SELECT 1 FROM campaign_deliveries d WHERE d.campaign_id = c.id AND d.status = 'RETRYING')
	AND NOT EXISTS (
		SELECT 1 FROM campaign_deliveries d
		WHERE d.campaign_id = c.id AND d.status IN ('SENT', 'FAILED', 'UNKNOWN', 'SKIPPED')
		  AND d.updated_at >= now() - make_interval(secs => $1))`

// PauseStalled stops campaigns that have got nowhere for StallAfter. Their messages
// stay as they are, so resuming carries on without anyone being messaged twice.
func (w *Worker) PauseStalled(ctx context.Context) error {
	if w.config.StallAfter <= 0 {
		return nil
	}
	rows, err := w.db.Query(ctx, `SELECT c.id::text FROM campaigns c WHERE`+stalledCondition, w.config.StallAfter.Seconds())
	if err != nil {
		return fmt.Errorf("find stalled campaigns: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("find stalled campaigns: %w", err)
	}
	var failures []error
	for _, id := range ids {
		failures = append(failures, w.pauseCampaign(ctx, id))
	}
	return errors.Join(failures...)
}

func (w *Worker) pauseCampaign(ctx context.Context, id string) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin pause: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Looked at again under the lock, in case another worker paused it or it recovered.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM campaigns WHERE id = $1 FOR UPDATE`, id); err != nil {
		return fmt.Errorf("lock campaign: %w", err)
	}
	var stalled bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns c WHERE c.id = $2 AND`+stalledCondition+`)`, w.config.StallAfter.Seconds(), id).Scan(&stalled); err != nil {
		return fmt.Errorf("check stalled campaign: %w", err)
	}
	if !stalled {
		return nil
	}
	var (
		waiting int
		reason  string
	)
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status IN ('QUEUED', 'RETRYING')),
		       COALESCE(mode() WITHIN GROUP (ORDER BY last_error) FILTER (WHERE status = 'RETRYING'), '')
		FROM campaign_deliveries WHERE campaign_id = $1`, id).Scan(&waiting, &reason); err != nil {
		return fmt.Errorf("read waiting deliveries: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = 'PAUSED', updated_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("pause campaign: %w", err)
	}
	details, err := json.Marshal(map[string]any{"waiting": waiting, "reason": reason, "after_minutes": int(w.config.StallAfter.Minutes())})
	if err != nil {
		return fmt.Errorf("encode audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (campaign_id, event_type, details) VALUES ($1, 'campaign.paused', $2)`, id, details); err != nil {
		return fmt.Errorf("record pause: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pause: %w", err)
	}
	slog.WarnContext(ctx, "paused a campaign that was getting nowhere", "campaign_id", id, "waiting", waiting, "reason", reason)
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
