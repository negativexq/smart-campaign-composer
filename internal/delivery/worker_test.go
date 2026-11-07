package delivery_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

const brief = "Send a 30% discount reminder to users in Istanbul who added a product to their cart in the last 30 days and did not buy."

var accepted = delivery.Result{StatusCode: http.StatusAccepted, MessageID: "m-1"}

// fakeSender answers from a function and records every call.
type fakeSender struct {
	mu      sync.Mutex
	calls   []delivery.Message
	respond func(call int, message delivery.Message) (delivery.Result, error)
}

func (f *fakeSender) Send(_ context.Context, message delivery.Message) (delivery.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, message)
	call := len(f.calls)
	f.mu.Unlock()
	return f.respond(call, message)
}

func always(result delivery.Result, err error) func(int, delivery.Message) (delivery.Result, error) {
	return func(int, delivery.Message) (delivery.Result, error) { return result, err }
}

// lockedRetry is how long the fake limiter has a locked channel wait.
const lockedRetry = 20 * time.Millisecond

type fakeLimiter struct {
	mu sync.Mutex
	// permits is how many were granted and not given back, per channel.
	permits map[string]int
	// answer overrides granting everything asked for.
	answer   func(channel string, want int) delivery.Permits
	err      error
	pauses   map[string]time.Duration
	pauseErr error
	// confirm, when set, is how many used permits settling allows.
	confirm int
	// settleErr, when set, is what settling fails with.
	settleErr error
	// locks stands in for the credential lock: its generation per locked channel,
	// and whether the channel's probe may go now.
	locks      map[string]int64
	probeDue   map[string]bool
	generation int64
}

func (f *fakeLimiter) LockCredentials(_ context.Context, channel string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, locked := f.locks[channel]; locked {
		return false, nil
	}
	if f.locks == nil {
		f.locks, f.probeDue = map[string]int64{}, map[string]bool{}
	}
	f.generation++
	f.locks[channel] = f.generation
	return true, nil
}

func (f *fakeLimiter) UnlockCredentials(_ context.Context, channel string, generation int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if current, locked := f.locks[channel]; !locked || current != generation {
		return false, nil
	}
	delete(f.locks, channel)
	return true, nil
}

func (f *fakeLimiter) ReleaseProbe(_ context.Context, channel string, generation int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locks[channel] == generation {
		f.probeDue[channel] = true
	}
	return nil
}

func (f *fakeLimiter) CredentialsLocked(_ context.Context, channel string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, locked := f.locks[channel]
	return locked, nil
}

// letProbe lets one probe through on a locked channel, as the probe interval ending would.
func (f *fakeLimiter) letProbe(channel string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeDue[channel] = true
}

func (f *fakeLimiter) locked(channel string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, locked := f.locks[channel]
	return locked
}

func (f *fakeLimiter) Pause(_ context.Context, channel string, length time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pauses == nil {
		f.pauses = map[string]time.Duration{}
	}
	f.pauses[channel] = length
	return f.pauseErr
}

func (f *fakeLimiter) Reserve(_ context.Context, channel string, want int) (delivery.Permits, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return delivery.Permits{}, f.err
	}
	answer := delivery.Permits{Granted: want}
	if f.answer != nil {
		answer = f.answer(channel, want)
	}
	if generation, locked := f.locks[channel]; locked {
		// The real limiter answers with the time left until the next probe. Here a
		// test makes the probe due with letProbe, so a fixed 30 s would leave the
		// worker asleep through it; a short wait keeps it looking, as the real
		// answer would wake it when the probe is due.
		if !f.probeDue[channel] {
			return delivery.Permits{Retry: lockedRetry, Paused: true}, nil
		}
		f.probeDue[channel] = false
		answer = delivery.Permits{Granted: 1, Probe: generation}
	}
	if f.permits == nil {
		f.permits = map[string]int{}
	}
	f.permits[channel] += answer.Granted
	return answer, nil
}

func (f *fakeLimiter) Answered(string, delivery.Permits, time.Time) {}

func (f *fakeLimiter) Settle(_ context.Context, channel string, permits delivery.Permits, used int, _ time.Duration) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settleErr != nil {
		return 0, f.settleErr
	}
	f.permits[channel] -= permits.Granted - used
	if f.confirm > 0 {
		return f.confirm, nil
	}
	return used, nil
}

type rig struct {
	pool     *pgxpool.Pool
	service  *campaign.Service
	id       string
	sender   *fakeSender
	limiter  *fakeLimiter
	metrics  *telemetry.Metrics
	worker   *delivery.Worker
	maxTries int
}

func newRig(t *testing.T, respond func(int, delivery.Message) (delivery.Result, error)) *rig {
	t.Helper()
	return newRigFor(t, campaign.NewCampaign{Brief: brief}, respond)
}

func newRigFor(t *testing.T, chosen campaign.NewCampaign, respond func(int, delivery.Message) (delivery.Result, error)) *rig {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewDatabase(t)
	now := time.Now()
	if _, err := seed.Apply(ctx, pool, seed.Generate(now, 13)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	metrics := telemetry.NewMetrics(prometheus.NewRegistry())
	mock := ai.NewMock()
	mock.StreamDelay = 0
	service := campaign.NewService(pool, mock, metrics, noop.NewTracerProvider().Tracer("test"), time.Now)

	id, err := service.CreateWith(ctx, chosen)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	generation, err := service.StartGeneration(ctx, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("start generation: %v", err)
	}
	if _, err := generation.Run(ctx, func(campaign.GenerationEvent) {}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := service.Dispatch(ctx, id); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	r := &rig{pool: pool, service: service, id: id, sender: &fakeSender{respond: respond}, limiter: &fakeLimiter{}, metrics: metrics, maxTries: 3}
	r.worker = r.newWorker()
	return r
}

func (r *rig) newWorker() *delivery.Worker {
	return delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		Unsubscribe: func(userID int64) string { return fmt.Sprintf("https://app.example.com/api/unsubscribe/%d", userID) },
		BatchSize:   50,
		Concurrency: 8,
		Lease:       time.Minute,
		MaxAttempts: r.maxTries,
		SendTimeout: time.Second,
		Backoff:     func(int) time.Duration { return 0 },
		AlertAfter:  10 * time.Minute,
	})
}

// drain works until nothing is due, as a bounded number of rounds.
func (r *rig) drain(t *testing.T) {
	t.Helper()
	for range 20 {
		claimed, err := r.worker.ProcessBatch(context.Background())
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if claimed == 0 {
			if err := r.worker.FinishCampaigns(context.Background()); err != nil {
				t.Fatalf("finish: %v", err)
			}
			return
		}
	}
	t.Fatal("deliveries were still due after 20 rounds")
}

func (r *rig) statuses(t *testing.T) map[string]int {
	t.Helper()
	progress, err := r.service.Progress(context.Background(), r.id)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	return progress.ByStatus
}

func (r *rig) campaignStatus(t *testing.T) string {
	t.Helper()
	view, err := r.service.Get(context.Background(), r.id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return view.Status
}

func (r *rig) queuedCount(t *testing.T) int {
	t.Helper()
	return r.statuses(t)["QUEUED"]
}

func TestAcceptedMessagesAreRecorded(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	queued := r.queuedCount(t)

	r.drain(t)

	if got := r.statuses(t); got["SENT"] != queued || got["QUEUED"] != 0 || got["SENDING"] != 0 {
		t.Fatalf("statuses: %v", got)
	}
	if len(r.sender.calls) != queued {
		t.Fatalf("calls: %d, want %d", len(r.sender.calls), queued)
	}
	if r.campaignStatus(t) != "COMPLETED" {
		t.Fatalf("campaign: %s", r.campaignStatus(t))
	}
	var attempts, sentAt, withID int
	if err := r.pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM delivery_attempts WHERE response_code = 202 AND outcome = 'accepted'),
		       count(sent_at), count(provider_message_id)
		FROM campaign_deliveries WHERE status = 'SENT'`).Scan(&attempts, &sentAt, &withID); err != nil {
		t.Fatal(err)
	}
	if attempts != queued || sentAt != queued || withID != queued {
		t.Fatalf("attempts %d, sent_at %d, ids %d, want %d", attempts, sentAt, withID, queued)
	}
	var completed int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE campaign_id = $1 AND event_type = 'campaign.completed'`, r.id).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("completed audits: %d", completed)
	}
	if got := testutil.ToFloat64(r.metrics.DeliveryAttempts.WithLabelValues("sms", "accepted")); got == 0 {
		t.Fatal("attempt metric not counted")
	}
}

func TestMessagesAreRenderedForEachPerson(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.drain(t)

	sawLongName, sawFallback := false, false
	for _, message := range r.sender.calls {
		if message.Channel != "sms" && message.Channel != "email" {
			t.Fatalf("channel: %+v", message)
		}
		if strings.Contains(message.Body+message.Subject, "{{") || message.To == "" || message.IdempotencyKey == "" {
			t.Fatalf("message: %+v", message)
		}
		if message.Channel == "sms" && message.Subject != "" {
			t.Fatalf("an SMS has no subject: %+v", message)
		}
		if message.Channel == "email" && message.Subject == "" {
			t.Fatalf("an email needs a subject: %+v", message)
		}
		sawLongName = sawLongName || strings.Contains(message.Body, "Maximiliano Alexander")
		sawFallback = sawFallback || strings.Contains(message.Body, "there") || strings.Contains(message.Body, "müşterimiz")
	}
	if !sawLongName || !sawFallback {
		t.Fatalf("long name %v, fallback %v", sawLongName, sawFallback)
	}
}

func TestConsentIsCheckedAgainJustBeforeSending(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	if _, err := r.pool.Exec(context.Background(), `UPDATE consents SET allowed = false WHERE user_id = $1 AND channel = 'sms'`, seed.FixtureCartNoPurchase); err != nil {
		t.Fatal(err)
	}
	r.drain(t)

	var status, reason string
	if err := r.pool.QueryRow(context.Background(), `SELECT status, last_error FROM campaign_deliveries WHERE user_id = $1 AND channel = 'sms'`, seed.FixtureCartNoPurchase).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "SKIPPED" || reason != "consent_withdrawn" {
		t.Fatalf("got %s %s", status, reason)
	}
	for _, message := range r.sender.calls {
		if strings.HasSuffix(message.IdempotencyKey, ":1:sms") {
			t.Fatal("someone who withdrew consent must not be messaged")
		}
	}
}

func TestTemporaryErrorsRetryWithTheSameKey(t *testing.T) {
	t.Parallel()
	seen := sync.Map{}
	r := newRig(t, func(_ int, message delivery.Message) (delivery.Result, error) {
		if _, again := seen.LoadOrStore(message.IdempotencyKey, true); again {
			return accepted, nil
		}
		return delivery.Result{StatusCode: http.StatusInternalServerError}, nil
	})
	queued := r.queuedCount(t)
	r.drain(t)

	if got := r.statuses(t); got["SENT"] != queued {
		t.Fatalf("statuses: %v", got)
	}
	if len(r.sender.calls) != 2*queued {
		t.Fatalf("calls: %d, want %d", len(r.sender.calls), 2*queued)
	}
	var wrong int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM campaign_deliveries WHERE status = 'SENT' AND attempts <> 2`).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if wrong != 0 {
		t.Fatalf("%d deliveries with the wrong attempt count", wrong)
	}
	if r.campaignStatus(t) != "COMPLETED" {
		t.Fatalf("campaign: %s", r.campaignStatus(t))
	}
}

func TestARetryWaitsForItsTurn(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusBadGateway}, nil))
	r.worker = delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		BatchSize: 100, Concurrency: 8, Lease: time.Minute, MaxAttempts: 3, SendTimeout: time.Second,
		Backoff: func(int) time.Duration { return time.Hour },
	})
	queued := r.queuedCount(t)
	if _, err := r.worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.statuses(t); got["RETRYING"] != queued {
		t.Fatalf("statuses: %v", got)
	}
	claimed, err := r.worker.ProcessBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if claimed != 0 {
		t.Fatalf("%d deliveries retried early", claimed)
	}
	if err := r.worker.FinishCampaigns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.campaignStatus(t) != "DISPATCHING" {
		t.Fatalf("a campaign with retries pending is not finished: %s", r.campaignStatus(t))
	}
}

// A scheduled run holds the campaign row while it adds deliveries. A worker that saw
// nothing open just before must not close the campaign over them, because nothing
// claims the deliveries of a campaign that is no longer sending.
func TestFinishingLeavesDeliveriesFromARunThatJustStarted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t, always(accepted, nil))
	r.drain(t)
	if _, err := r.pool.Exec(ctx, `UPDATE campaigns SET status = 'DISPATCHING' WHERE id = $1`, r.id); err != nil {
		t.Fatal(err)
	}

	run, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := run.Exec(ctx, `SELECT 1 FROM campaigns WHERE id = $1 FOR UPDATE`, r.id); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Exec(ctx, `
		UPDATE campaign_deliveries SET status = 'QUEUED', next_attempt_at = now()
		WHERE id = (SELECT id FROM campaign_deliveries WHERE campaign_id = $1 AND status = 'SENT' ORDER BY id LIMIT 1)`, r.id); err != nil {
		t.Fatal(err)
	}

	finished := make(chan error, 1)
	go func() { finished <- r.worker.FinishCampaigns(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := r.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			               WHERE datname = current_database() AND wait_event_type = 'Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finishing never waited for the run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := run.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}

	if status := r.campaignStatus(t); status != "DISPATCHING" {
		t.Fatalf("closed as %s with a delivery still queued", status)
	}
	r.drain(t)
	if got := r.statuses(t); got["QUEUED"] != 0 {
		t.Fatalf("the delivery was never sent: %v", got)
	}
	if status := r.campaignStatus(t); status != "COMPLETED" {
		t.Fatalf("status after the run: %s", status)
	}
}

// rests makes everything look as if the last news came long ago.
func (r *rig) rests(t *testing.T, length time.Duration) {
	t.Helper()
	ctx := context.Background()
	for _, query := range []string{
		`UPDATE campaigns SET status_since = now() - make_interval(secs => $2) WHERE id = $1`,
		`UPDATE campaign_deliveries SET updated_at = now() - make_interval(secs => $2) WHERE campaign_id = $1`,
	} {
		if _, err := r.pool.Exec(ctx, query, r.id, length.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *rig) stallingWorker() *delivery.Worker {
	return delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		Unsubscribe: func(userID int64) string { return fmt.Sprintf("https://app.example.com/api/unsubscribe/%d", userID) },
		BatchSize:   50, Concurrency: 8, Lease: time.Minute, MaxAttempts: 3, SendTimeout: time.Second,
		Backoff: func(int) time.Duration { return time.Hour }, StallAfter: 10 * time.Minute,
	})
}

func TestACampaignGettingNowhereInAnOutageStopsAndCanResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusTooManyRequests}, nil))
	worker := r.stallingWorker()
	if _, err := worker.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}

	if err := worker.PauseStalled(ctx); err != nil {
		t.Fatal(err)
	}
	if status := r.campaignStatus(t); status != "DISPATCHING" {
		t.Fatalf("stopped too soon: %s", status)
	}

	r.rests(t, 11*time.Minute)
	if err := worker.PauseStalled(ctx); err != nil {
		t.Fatal(err)
	}
	if status := r.campaignStatus(t); status != "PAUSED" {
		t.Fatalf("still %s after the outage", status)
	}
	var details string
	if err := r.pool.QueryRow(ctx, `SELECT details::text FROM audit_events WHERE campaign_id = $1 AND event_type = 'campaign.paused'`, r.id).Scan(&details); err != nil {
		t.Fatalf("no record of the pause: %v", err)
	}
	if !strings.Contains(details, "rate_limited") {
		t.Fatalf("details: %s", details)
	}

	// Nothing is lost and nothing is claimed while it is stopped.
	before := len(r.sender.calls)
	if _, err := r.pool.Exec(ctx, `UPDATE campaign_deliveries SET next_attempt_at = now() WHERE campaign_id = $1`, r.id); err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.ProcessBatch(ctx); err != nil || claimed != 0 {
		t.Fatalf("claimed %d while paused: %v", claimed, err)
	}
	if got := r.statuses(t); got["FAILED"] != 0 || got["RETRYING"] == 0 {
		t.Fatalf("statuses: %v", got)
	}
	if len(r.sender.calls) != before {
		t.Fatal("called the provider while paused")
	}

	r.sender.respond = always(accepted, nil)
	if _, err := r.service.Resume(ctx, r.id); err != nil {
		t.Fatal(err)
	}
	if err := worker.PauseStalled(ctx); err != nil {
		t.Fatal(err)
	}
	if status := r.campaignStatus(t); status != "DISPATCHING" {
		t.Fatalf("resuming started no new wait: %s", status)
	}
	r.drain(t)
	if status := r.campaignStatus(t); status != "COMPLETED" {
		t.Fatalf("status after resuming: %s, %v", status, r.statuses(t))
	}
}

func TestRenamingAStalledCampaignDoesNotPutOffItsPause(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusTooManyRequests}, nil))
	worker := r.stallingWorker()
	if _, err := worker.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	r.rests(t, 11*time.Minute)
	// A rename or a tag moves updated_at, which used to be the stall clock.
	if _, err := r.service.Rename(ctx, r.id, "Renamed while stuck"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.service.SetTags(ctx, r.id, []string{"stuck"}); err != nil {
		t.Fatal(err)
	}
	if err := worker.PauseStalled(ctx); err != nil {
		t.Fatal(err)
	}
	if status := r.campaignStatus(t); status != "PAUSED" {
		t.Fatalf("a rename put off the pause: %s", status)
	}
}

func TestACampaignThatKeepsFinishingMessagesIsNotStopped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusTooManyRequests}, nil))
	worker := r.stallingWorker()
	if _, err := worker.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	r.rests(t, 11*time.Minute)
	if _, err := r.pool.Exec(ctx, `
		UPDATE campaign_deliveries SET status = 'SENT', updated_at = now()
		WHERE id = (SELECT id FROM campaign_deliveries WHERE campaign_id = $1 AND status = 'RETRYING' ORDER BY id LIMIT 1)`, r.id); err != nil {
		t.Fatal(err)
	}
	if err := worker.PauseStalled(ctx); err != nil {
		t.Fatal(err)
	}
	if status := r.campaignStatus(t); status != "DISPATCHING" {
		t.Fatalf("stopped although a message just finished: %s", status)
	}
}

func TestACampaignOnlyWaitingBehindAnotherIsNotStopped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t, always(accepted, nil))
	r.rests(t, 11*time.Minute)
	worker := r.stallingWorker()
	if err := worker.PauseStalled(ctx); err != nil {
		t.Fatal(err)
	}
	if status := r.campaignStatus(t); status != "DISPATCHING" {
		t.Fatalf("stopped although nothing went wrong: %s", status)
	}
}

func TestServerErrorsEndAsFailed(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusServiceUnavailable}, nil))
	queued := r.queuedCount(t)
	r.drain(t)

	if got := r.statuses(t); got["FAILED"] != queued {
		t.Fatalf("statuses: %v", got)
	}
	if len(r.sender.calls) != queued*r.maxTries {
		t.Fatalf("calls: %d", len(r.sender.calls))
	}
	if r.campaignStatus(t) != "COMPLETED_WITH_FAILURES" {
		t.Fatalf("campaign: %s", r.campaignStatus(t))
	}
}

func TestARejectionIsNotRetried(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusBadRequest}, nil))
	queued := r.queuedCount(t)
	r.drain(t)

	if got := r.statuses(t); got["FAILED"] != queued || len(r.sender.calls) != queued {
		t.Fatalf("statuses %v, calls %d", got, len(r.sender.calls))
	}
}

func TestTimeoutsEndAsUnknownNotFailed(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{}, context.DeadlineExceeded))
	queued := r.queuedCount(t)
	r.drain(t)

	if got := r.statuses(t); got["UNKNOWN"] != queued {
		t.Fatalf("the provider may have accepted these: %v", got)
	}
	if r.campaignStatus(t) != "COMPLETED_WITH_FAILURES" {
		t.Fatalf("campaign: %s", r.campaignStatus(t))
	}
}

func TestAnEarlierTimeoutKeepsTheOutcomeUnknown(t *testing.T) {
	t.Parallel()
	seen := sync.Map{}
	r := newRig(t, func(_ int, message delivery.Message) (delivery.Result, error) {
		if _, again := seen.LoadOrStore(message.IdempotencyKey, true); !again {
			return delivery.Result{}, errors.New("connection reset")
		}
		return delivery.Result{StatusCode: http.StatusInternalServerError}, nil
	})
	queued := r.queuedCount(t)
	r.drain(t)

	if got := r.statuses(t); got["UNKNOWN"] != queued {
		t.Fatalf("the first attempt may have got through: %v", got)
	}
}

func TestRateLimitedCallsDoNotUseUpAttempts(t *testing.T) {
	t.Parallel()
	seen := sync.Map{}
	r := newRig(t, func(_ int, message delivery.Message) (delivery.Result, error) {
		count, _ := seen.LoadOrStore(message.IdempotencyKey, new(int))
		tries := count.(*int)
		*tries++
		if *tries <= 5 { // more than MaxAttempts
			return delivery.Result{StatusCode: http.StatusTooManyRequests}, nil
		}
		return accepted, nil
	})
	queued := r.queuedCount(t)
	r.drain(t)

	if got := r.statuses(t); got["SENT"] != queued {
		t.Fatalf("statuses: %v", got)
	}
	var wrong, rows int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE attempts <> 1) FROM campaign_deliveries WHERE status = 'SENT'`).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM delivery_attempts WHERE outcome = 'rate_limited' AND response_code = 429`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if wrong != 0 || rows != 5*queued {
		t.Fatalf("wrong attempts %d, 429 rows %d", wrong, rows)
	}
}

func TestRetryAfterIsRespected(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusTooManyRequests, RetryAfter: time.Hour}, nil))
	if _, err := r.worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var early int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM campaign_deliveries WHERE status = 'RETRYING' AND next_attempt_at < now() + interval '50 minutes'`).Scan(&early); err != nil {
		t.Fatal(err)
	}
	if early != 0 {
		t.Fatalf("%d deliveries ignored Retry-After", early)
	}
}

func TestParallelWorkersSendEachDeliveryOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	queued := r.queuedCount(t)
	var wg sync.WaitGroup
	for range 4 {
		worker := r.newWorker()
		wg.Go(func() {
			for {
				claimed, err := worker.ProcessBatch(context.Background())
				if err != nil {
					t.Errorf("process: %v", err)
					return
				}
				if claimed == 0 {
					return
				}
			}
		})
	}
	wg.Wait()

	if len(r.sender.calls) != queued {
		t.Fatalf("calls: %d, want %d", len(r.sender.calls), queued)
	}
	keys := map[string]bool{}
	for _, message := range r.sender.calls {
		if keys[message.IdempotencyKey] {
			t.Fatalf("sent twice: %s", message.IdempotencyKey)
		}
		keys[message.IdempotencyKey] = true
	}
}

func TestAWorkerThatLostItsClaimRecordsNothing(t *testing.T) {
	t.Parallel()
	var r *rig
	r = newRig(t, func(_ int, _ delivery.Message) (delivery.Result, error) {
		// Another worker took over after this one's lease ran out.
		_, err := r.pool.Exec(context.Background(), `UPDATE campaign_deliveries SET claim_token = gen_random_uuid() WHERE status = 'SENDING'`)
		return accepted, err
	})
	if _, err := r.worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var sent, attempts int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE status = 'SENT'), (SELECT count(*) FROM delivery_attempts) FROM campaign_deliveries`).Scan(&sent, &attempts); err != nil {
		t.Fatal(err)
	}
	if sent != 0 || attempts != 0 {
		t.Fatalf("sent %d, attempts %d", sent, attempts)
	}
}

func TestEverySendPassesTheLimiter(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	view, err := r.service.Get(context.Background(), r.id)
	if err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	if r.limiter.permits["sms"] != view.Audience.SMS || r.limiter.permits["email"] != view.Audience.Email {
		t.Fatalf("permits: %v, audience: %+v", r.limiter.permits, view.Audience)
	}
}

func TestNoLimiterMeansNoSending(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	queued := r.queuedCount(t)
	r.limiter.err = errors.New("redis is down")
	r.worker = delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		BatchSize: 100, Concurrency: 8, Lease: time.Minute, MaxAttempts: 3, SendTimeout: time.Second,
		Backoff: func(int) time.Duration { return 0 },
	})
	if _, err := r.worker.ProcessBatch(context.Background()); err == nil {
		t.Fatal("the limiter being down must be reported")
	}
	if len(r.sender.calls) != 0 {
		t.Fatalf("calls: %d", len(r.sender.calls))
	}
	if got := r.statuses(t); got["QUEUED"] != queued || got["SENDING"] != 0 {
		t.Fatalf("nothing should have been claimed: %v", got)
	}
	var attempts int
	if err := r.pool.QueryRow(context.Background(), `SELECT COALESCE(sum(attempts), 0) FROM campaign_deliveries`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("the limiter being down must not use up attempts: %d", attempts)
	}
}

func TestDuplicatesReportedByTheProviderAreCounted(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusOK, MessageID: "m-1", Duplicate: true}, nil))
	r.drain(t)
	if got := testutil.ToFloat64(r.metrics.DeliveryDuplicates.WithLabelValues("sms")); got == 0 {
		t.Fatal("not counted")
	}
}

func TestARepeatingCampaignWaitsForItsNextRun(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `INSERT INTO campaign_schedules (campaign_id, spec, next_run_at) VALUES ($1, '{"kind":"interval","every_hours":24}', now() + interval '1 day')`, r.id); err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	if r.campaignStatus(t) != "SCHEDULED" {
		t.Fatalf("campaign: %s", r.campaignStatus(t))
	}
	var finished, completed int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type = 'campaign.run_finished'), count(*) FILTER (WHERE event_type = 'campaign.completed') FROM audit_events WHERE campaign_id = $1`, r.id).Scan(&finished, &completed); err != nil {
		t.Fatal(err)
	}
	if finished != 1 || completed != 0 {
		t.Fatalf("finished %d, completed %d", finished, completed)
	}
}

// A finished run keeps what it counted, so reading it later needs no recount.
func TestAFinishedRunKeepsItsCounts(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	ctx := context.Background()
	r.drain(t)

	var finished bool
	var total int
	var counts, responses, recounted, recalled []byte
	if err := r.pool.QueryRow(ctx, `
		SELECT r.finished_at IS NOT NULL, r.total, r.counts, r.responses,
		       (SELECT jsonb_agg(jsonb_build_array(channel, status, n, a) ORDER BY channel, status)
		        FROM (SELECT channel, status, count(*) AS n, sum(attempts) AS a FROM campaign_deliveries WHERE run_id = r.id GROUP BY 1, 2) s),
		       (SELECT jsonb_object_agg(code, n)
		        FROM (SELECT response_code::text AS code, count(*) AS n FROM delivery_attempts WHERE run_id = r.id AND response_code IS NOT NULL GROUP BY 1) s)
		FROM campaign_runs r WHERE r.campaign_id = $1`, r.id).Scan(&finished, &total, &counts, &responses, &recounted, &recalled); err != nil {
		t.Fatal(err)
	}
	if !finished || total == 0 || string(counts) != string(recounted) || string(responses) != string(recalled) {
		t.Fatalf("finished %v total %d counts %s (recounted %s) responses %s (recalled %s)", finished, total, counts, recounted, responses, recalled)
	}
}

// crash leaves deliveries as a killed worker would: claimed, with the lease run out.
func (r *rig) crash(t *testing.T, count, attempts int, lease string) {
	t.Helper()
	_, err := r.pool.Exec(context.Background(), `
		UPDATE campaign_deliveries
		SET status = 'SENDING', attempts = $2, claim_token = gen_random_uuid(), lease_expires_at = now() + $3::interval
		WHERE id IN (SELECT id FROM campaign_deliveries WHERE status = 'QUEUED' ORDER BY id LIMIT $1)`, count, attempts, lease)
	if err != nil {
		t.Fatalf("simulate crash: %v", err)
	}
}

func TestMessagesLeftByACrashedWorkerAreSentAgain(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.crash(t, 5, 0, "-1 second")

	recovered, err := r.worker.RecoverExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 5 {
		t.Fatalf("recovered %d, want 5", recovered)
	}
	r.drain(t)
	if got := r.statuses(t); got["SENDING"] != 0 || got["RETRYING"] != 0 || got["QUEUED"] != 0 {
		t.Fatalf("statuses after recovery: %v", got)
	}
	if status := r.campaignStatus(t); status != "COMPLETED" {
		t.Fatalf("campaign is %s", status)
	}
	var lost int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM delivery_attempts WHERE outcome = 'timeout' AND response_code IS NULL`).Scan(&lost); err != nil {
		t.Fatal(err)
	}
	if lost != 5 {
		t.Fatalf("the unfinished calls must stay on record: %d", lost)
	}
}

func TestALeaseStillRunningIsLeftAlone(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.crash(t, 5, 0, "1 minute")
	recovered, err := r.worker.RecoverExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 0 || r.statuses(t)["SENDING"] != 5 {
		t.Fatalf("recovered %d, statuses %v", recovered, r.statuses(t))
	}
}

func TestAMessageThatKeepsCrashingWorkersEndsAsUnknown(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.crash(t, 2, r.maxTries-1, "-1 second")
	if _, err := r.worker.RecoverExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.statuses(t)["UNKNOWN"]; got != 2 {
		t.Fatalf("a message must not crash workers forever: %v", r.statuses(t))
	}
}

func TestACrashKeepsALaterFailureUnknown(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusInternalServerError}, nil))
	r.crash(t, 1, 0, "-1 second")
	if _, err := r.worker.RecoverExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	got := r.statuses(t)
	if got["UNKNOWN"] != 1 {
		t.Fatalf("the crashed call may have gone through: %v", got)
	}
	if got["FAILED"] == 0 {
		t.Fatalf("others should plainly fail: %v", got)
	}
}

func TestARecoveredMessageKeepsItsIdempotencyKey(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.crash(t, 1, 0, "-1 second")
	var key string
	if err := r.pool.QueryRow(context.Background(), `SELECT idempotency_key FROM campaign_deliveries WHERE status = 'SENDING'`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if _, err := r.worker.RecoverExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	calls := 0
	for _, message := range r.sender.calls {
		if message.IdempotencyKey == key {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("key used %d times by the new worker", calls)
	}
}

func TestParallelRecoveryCountsEachMessageOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.crash(t, 20, 0, "-1 second")
	crashed := r.statuses(t)["SENDING"]
	if crashed < 2 {
		t.Fatalf("only %d deliveries to recover", crashed)
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)
	for range 4 {
		wg.Go(func() {
			recovered, err := r.newWorker().RecoverExpired(context.Background())
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += recovered
			mu.Unlock()
		})
	}
	wg.Wait()
	var rows int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM delivery_attempts`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if total != crashed || rows != crashed {
		t.Fatalf("crashed %d, recovered %d, attempts %d", crashed, total, rows)
	}
}

func TestARateLimitedCallPausesTheChannel(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusTooManyRequests, RetryAfter: 3 * time.Second}, nil))
	if _, err := r.worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.limiter.pauses) == 0 {
		t.Fatal("other workers must learn about the 429")
	}
	for channel, length := range r.limiter.pauses {
		if length != 3*time.Second {
			t.Errorf("%s paused for %v", channel, length)
		}
	}
}

func TestAFailedPauseStillRecordsTheOutcome(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{StatusCode: http.StatusTooManyRequests, RetryAfter: time.Second}, nil))
	r.limiter.pauseErr = errors.New("redis is down")
	if _, err := r.worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.statuses(t)["SENDING"] != 0 || r.statuses(t)["RETRYING"] == 0 {
		t.Fatalf("statuses: %v", r.statuses(t))
	}
}

func TestAProviderThatCannotBeReachedCostsNoAttempt(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(delivery.Result{}, fmt.Errorf("call provider: %w", delivery.ErrNotSent)))
	if _, err := r.worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	var attempts, rows int
	if err := r.pool.QueryRow(context.Background(), `SELECT COALESCE(sum(attempts), 0), (SELECT count(*) FROM delivery_attempts) FROM campaign_deliveries`).Scan(&attempts, &rows); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || rows != 0 {
		t.Fatalf("attempts %d, rows %d: nothing was sent", attempts, rows)
	}
	if r.statuses(t)["RETRYING"] == 0 {
		t.Fatalf("statuses: %v", r.statuses(t))
	}
	if len(r.limiter.pauses) == 0 {
		t.Fatal("the channel should rest while the provider is down")
	}
}

// The other recovery tests call RecoverExpired directly. This one proves a running
// worker does it by itself, which is what a real crash depends on.
func TestARunningWorkerRecoversAndFinishesByItself(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.crash(t, 5, 0, "-1 second")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- r.worker.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for r.campaignStatus(t) != "COMPLETED" {
		if time.Now().After(deadline) {
			t.Fatalf("campaign is %s with %v", r.campaignStatus(t), r.statuses(t))
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

func TestAnEmailGoesOutDesignedWithAWayToUnsubscribe(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.drain(t)

	emails := 0
	for _, message := range r.sender.calls {
		if message.Channel != "email" {
			if message.HTML != "" || message.UnsubscribeURL != "" {
				t.Fatalf("an SMS has no design: %+v", message)
			}
			continue
		}
		emails++
		if !strings.HasPrefix(message.UnsubscribeURL, "https://app.example.com/api/unsubscribe/") {
			t.Fatalf("unsubscribe: %q", message.UnsubscribeURL)
		}
		if !strings.Contains(message.HTML, "<table") || !strings.Contains(message.HTML, message.UnsubscribeURL) {
			t.Fatalf("html: %s", message.HTML)
		}
		if !strings.Contains(message.Body, ": "+message.UnsubscribeURL) || strings.Contains(message.Body, "<table") {
			t.Fatalf("plain text: %s", message.Body)
		}
	}
	if emails == 0 {
		t.Fatal("no email was sent")
	}
}

// A provider that never answers is the slowest stop there is: the worker must wait
// for the call, record it and only then return, leaving nothing in flight.
func TestAStopWaitsForACallThatGetsNoAnswer(t *testing.T) {
	t.Parallel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	release := make(chan struct{})
	var once sync.Once
	r := newRig(t, func(int, delivery.Message) (delivery.Result, error) {
		once.Do(func() {
			stop()
			close(release)
		})
		<-release
		return delivery.Result{}, errors.New("no answer")
	})

	if err := r.worker.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := r.statuses(t); got["SENDING"] != 0 {
		t.Fatalf("left in flight: %v", got)
	}
}

// Whatever runs the worker must wait out MaxDrain, or a stop becomes a kill that
// strands messages. This keeps the two numbers from drifting apart.
func TestComposeWaitsLongerThanAWorkerCanTakeToStop(t *testing.T) {
	t.Parallel()
	file, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`(?m)^  worker:\n(?:    .*\n|      .*\n)*?    stop_grace_period: (\w+)`).FindSubmatch(file)
	if found == nil {
		t.Fatal("the worker service sets no stop_grace_period")
	}
	grace, err := time.ParseDuration(string(found[1]))
	if err != nil {
		t.Fatal(err)
	}
	if grace <= delivery.MaxDrain {
		t.Fatalf("stop_grace_period %v must be longer than %v", grace, delivery.MaxDrain)
	}
}

func TestEachPersonReadsTheirOwnLanguage(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.drain(t)

	languages := map[string]string{}
	rows, err := r.pool.Query(context.Background(), `SELECT email, phone, language FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var email, phone, language string
		if err := rows.Scan(&email, &phone, &language); err != nil {
			t.Fatal(err)
		}
		languages[email], languages[phone] = language, language
	}

	seen := map[string]bool{}
	for _, message := range r.sender.calls {
		if message.Channel != "email" {
			continue
		}
		language := languages[message.To]
		seen[language] = true
		want := map[string]string{"tr": "Abonelikten çık", "en": "Unsubscribe"}[language]
		if !strings.Contains(message.Body, want) {
			t.Fatalf("%s reader got: %s", language, message.Body)
		}
	}
	if !seen["tr"] || !seen["en"] {
		t.Fatalf("languages sent: %v", seen)
	}
}

func TestEachPersonReadsTheirSideOfATest(t *testing.T) {
	t.Parallel()
	r := newRigFor(t, campaign.NewCampaign{Brief: brief, ABTest: true}, always(accepted, nil))
	r.drain(t)

	sides := map[string]string{}
	rows, err := r.pool.Query(context.Background(), `
		SELECT CASE d.channel WHEN 'sms' THEN u.phone ELSE u.email END, d.variant
		FROM campaign_deliveries d JOIN users u ON u.id = d.user_id WHERE d.campaign_id = $1`, r.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var to, side string
		if err := rows.Scan(&to, &side); err != nil {
			t.Fatal(err)
		}
		sides[to] = side
	}

	texts := map[string]map[string]bool{"A": {}, "B": {}}
	for _, message := range r.sender.calls {
		if message.Channel == "sms" {
			texts[sides[message.To]][message.Body] = true
		}
	}
	if len(texts["A"]) == 0 || len(texts["B"]) == 0 {
		t.Fatalf("sides sent: %d, %d", len(texts["A"]), len(texts["B"]))
	}
	for body := range texts["A"] {
		if texts["B"][body] {
			t.Fatalf("both sides read: %s", body)
		}
	}
}

// A call that gets no answer takes up one slot until it times out. Everything else
// must keep going around it, or one lost response stops the whole queue.
func TestACallThatGetsNoAnswerDoesNotHoldUpTheOthers(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var once sync.Once
	r := newRig(t, func(int, delivery.Message) (delivery.Result, error) {
		blocked := false
		once.Do(func() { blocked = true })
		if blocked {
			<-release
		}
		return accepted, nil
	})
	queued := r.queuedCount(t)
	if queued <= 8 {
		t.Fatalf("need more deliveries than one round holds, have %d", queued)
	}
	worker := delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		BatchSize: 4, Concurrency: 4, Lease: time.Minute, MaxAttempts: 3, SendTimeout: time.Minute,
		Backoff: func(int) time.Duration { return 0 },
	})

	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	defer func() {
		stop()
		close(release)
		if err := <-stopped; err != nil {
			t.Errorf("run: %v", err)
		}
		if got := r.statuses(t); got["SENDING"] != 0 {
			t.Errorf("left in flight: %v", got)
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r.statuses(t)["SENT"] == queued-1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the others did not finish: %v", r.statuses(t))
}

func TestRunStaysWithinItsConcurrency(t *testing.T) {
	t.Parallel()
	const limit = 3
	var (
		mu      sync.Mutex
		running = map[string]int{}
		worst   int
	)
	r := newRig(t, func(_ int, message delivery.Message) (delivery.Result, error) {
		mu.Lock()
		running[message.Channel]++
		worst = max(worst, running[message.Channel])
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		running[message.Channel]--
		mu.Unlock()
		return accepted, nil
	})
	queued := r.queuedCount(t)
	worker := delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		BatchSize: 50, Concurrency: limit, Lease: time.Minute, MaxAttempts: 3, SendTimeout: time.Minute,
		Backoff: func(int) time.Duration { return 0 },
	})

	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	deadline := time.Now().Add(30 * time.Second)
	for r.statuses(t)["SENT"] < queued && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if err := <-stopped; err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := r.statuses(t)["SENT"]; got != queued {
		t.Fatalf("sent %d of %d", got, queued)
	}
	if worst > limit {
		t.Fatalf("%d calls at once on a channel, limit %d", worst, limit)
	}
}

// Overview counts calls per channel from the attempts alone.
func TestAttemptsCarryTheirDeliverysChannel(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.drain(t)
	if _, err := r.pool.Exec(context.Background(), `
		UPDATE campaign_deliveries SET status = 'SENDING', lease_expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.worker.RecoverExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	var total, wrong int
	if err := r.pool.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE a.channel <> d.channel)
		FROM delivery_attempts a JOIN campaign_deliveries d ON d.id = a.delivery_id`).Scan(&total, &wrong); err != nil {
		t.Fatal(err)
	}
	if total == 0 || wrong != 0 {
		t.Fatalf("attempts %d, on the wrong channel %d", total, wrong)
	}
}

// A channel with no permission claims nothing: its deliveries hold no slots and no
// leases, and the other channel carries on as if it were not there.
func TestAPausedChannelDoesNotHoldUpTheOther(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.limiter.answer = func(channel string, want int) delivery.Permits {
		if channel == "email" {
			return delivery.Permits{Retry: 10 * time.Millisecond, Paused: true}
		}
		return delivery.Permits{Granted: want}
	}
	count := func(channel, status string) int {
		var n int
		if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM campaign_deliveries WHERE channel = $1 AND status = $2`, channel, status).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	sms, email := count("sms", "QUEUED"), count("email", "QUEUED")
	if sms == 0 || email == 0 {
		t.Fatalf("need both channels: sms %d, email %d", sms, email)
	}

	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- r.worker.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for count("sms", "SENT") < sms && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if err := <-stopped; err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := count("sms", "SENT"); got != sms {
		t.Fatalf("sms sent %d of %d", got, sms)
	}
	if got := count("email", "QUEUED"); got != email {
		t.Fatalf("email should wait unclaimed: %d of %d queued", got, email)
	}
}

func TestClaimsNoMoreThanItHasPermission(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.limiter.answer = func(string, int) delivery.Permits { return delivery.Permits{Granted: 2} }
	claimed, err := r.worker.ProcessBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if claimed != 4 || len(r.sender.calls) != 4 {
		t.Fatalf("claimed %d, calls %d, want 2 per channel", claimed, len(r.sender.calls))
	}
}

func TestPermitsNotNeededAreGivenBack(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.drain(t)
	calls := map[string]int{}
	for _, message := range r.sender.calls {
		calls[message.Channel]++
	}
	for _, channel := range []string{"sms", "email"} {
		if r.limiter.permits[channel] != calls[channel] {
			t.Fatalf("%s kept %d permits for %d calls", channel, r.limiter.permits[channel], calls[channel])
		}
	}
}

// A claim that outlasted its permits holds messages nothing allows sending yet. They
// go back in the queue and lose no attempt.
func TestAClaimWithoutConfirmedPermitsReturnsToTheQueue(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.limiter.confirm = 1
	ctx := context.Background()
	const batches = 2 // one per channel
	sent, err := r.worker.ProcessBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sent != batches || len(r.sender.calls) != batches {
		t.Fatalf("claimed %d, called %d", sent, len(r.sender.calls))
	}
	var sending, returned, attempts int
	err = r.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'SENDING'), count(*) FILTER (WHERE status = 'RETRYING'),
		coalesce(sum(attempts) FILTER (WHERE status = 'RETRYING'), 0) FROM campaign_deliveries WHERE campaign_id = $1`, r.id).
		Scan(&sending, &returned, &attempts)
	if err != nil {
		t.Fatal(err)
	}
	if sending != 0 || returned == 0 || attempts != 0 {
		t.Fatalf("sending %d, returned %d, attempts %d", sending, returned, attempts)
	}
}

// Redis stopping between a permit and its use must stop the send, however late it comes.
func TestAClaimThatCannotBeSettledIsNotSent(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.limiter.settleErr = errors.New("redis is down")
	ctx := context.Background()
	if _, err := r.worker.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.sender.calls) != 0 {
		t.Fatalf("%d messages went out without the rate limiter", len(r.sender.calls))
	}
	var sending, returned, attempts int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'SENDING'), count(*) FILTER (WHERE status = 'RETRYING'),
		coalesce(sum(attempts), 0) FROM campaign_deliveries WHERE campaign_id = $1`, r.id).Scan(&sending, &returned, &attempts)
	if err != nil {
		t.Fatal(err)
	}
	if sending != 0 || returned == 0 || attempts != 0 {
		t.Fatalf("sending %d, returned %d, attempts %d", sending, returned, attempts)
	}
}

// A probe that never went out says nothing about the provider. If it were lost, the
// breaker would wait for its result forever and the channel would never send again.
func TestAProbeThatCannotBeSettledIsNotLost(t *testing.T) {
	t.Parallel()
	var down atomic.Bool
	down.Store(true)
	r := newRig(t, func(int, delivery.Message) (delivery.Result, error) {
		if down.Load() {
			return delivery.Result{StatusCode: http.StatusBadGateway}, nil
		}
		return accepted, nil
	})
	queued := r.queuedCount(t)
	worker := delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		BatchSize: 50, Concurrency: 4, Lease: time.Minute, MaxAttempts: 1000, SendTimeout: time.Second,
		Backoff: func(int) time.Duration { return 0 },
		Breaker: delivery.BreakerConfig{Window: 10 * time.Second, MinCalls: 3, FailureRatio: 0.5, FirstWait: 10 * time.Millisecond, MaxWait: 40 * time.Millisecond, Probes: 2},
	})
	setSettleErr := func(err error) {
		r.limiter.mu.Lock()
		defer r.limiter.mu.Unlock()
		r.limiter.settleErr = err
	}

	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	defer func() {
		stop()
		if err := <-stopped; err != nil {
			t.Errorf("run: %v", err)
		}
	}()

	time.Sleep(time.Second) // both channels trip and start probing
	setSettleErr(errors.New("redis is down"))
	time.Sleep(time.Second) // every probe is claimed and handed back
	setSettleErr(nil)
	down.Store(false)

	deadline := time.Now().Add(10 * time.Second)
	for r.statuses(t)["SENT"] < queued && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := r.statuses(t); got["SENT"] != queued {
		t.Fatalf("after the outage: %v, want %d sent", got, queued)
	}
}

// A provider answering 5xx for a while says nothing about the messages, so they must
// not use up their attempts on it, however long it lasts.
func TestAnOutageOfServerErrorsIsWaitedOut(t *testing.T) {
	t.Parallel()
	var down atomic.Bool
	down.Store(true)
	r := newRig(t, func(int, delivery.Message) (delivery.Result, error) {
		if down.Load() {
			return delivery.Result{StatusCode: http.StatusBadGateway}, nil
		}
		return accepted, nil
	})
	queued := r.queuedCount(t)
	worker := delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		BatchSize: 50, Concurrency: 4, Lease: time.Minute, MaxAttempts: r.maxTries, SendTimeout: time.Second,
		Backoff: func(int) time.Duration { return 0 },
		Breaker: delivery.BreakerConfig{Window: 10 * time.Second, MinCalls: 3, FailureRatio: 0.5, FirstWait: 10 * time.Millisecond, MaxWait: 40 * time.Millisecond, Probes: 2},
	})
	attempts := func() int {
		var n int
		if err := r.pool.QueryRow(context.Background(), `SELECT COALESCE(sum(attempts), 0) FROM campaign_deliveries`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	defer func() {
		stop()
		if err := <-stopped; err != nil {
			t.Errorf("run: %v", err)
		}
	}()

	// Attempts stop once both channels have tripped, however long that takes here.
	before, settled := attempts(), time.Now().Add(10*time.Second)
	for still := 0; still < 3 && time.Now().Before(settled); {
		time.Sleep(100 * time.Millisecond)
		now := attempts()
		if now == before && now > 0 {
			still++
		} else {
			still = 0
		}
		before = now
	}
	time.Sleep(500 * time.Millisecond) // several probes go out and fail
	if got := attempts(); got != before {
		t.Errorf("attempts went from %d to %d during the outage", before, got)
	}
	if got := r.statuses(t); got["FAILED"] != 0 || got["UNKNOWN"] != 0 {
		t.Fatalf("messages were given up on during an outage: %v", got)
	}

	down.Store(false)
	deadline := time.Now().Add(10 * time.Second)
	for r.statuses(t)["SENT"] < queued && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := r.statuses(t); got["SENT"] != queued {
		t.Fatalf("after the outage: %v, want %d sent", got, queued)
	}
}
