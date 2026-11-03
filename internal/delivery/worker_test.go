package delivery_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
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

type fakeLimiter struct {
	mu    sync.Mutex
	waits map[string]int
	err   error
}

func (f *fakeLimiter) Wait(_ context.Context, channel string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.waits == nil {
		f.waits = map[string]int{}
	}
	f.waits[channel]++
	return 0, f.err
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

	id, err := service.Create(ctx, brief)
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
		BatchSize:   50,
		Concurrency: 8,
		Lease:       time.Minute,
		MaxAttempts: r.maxTries,
		SendTimeout: time.Second,
		Backoff:     func(int) time.Duration { return 0 },
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
		sawFallback = sawFallback || strings.Contains(message.Body, "there")
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
	if r.limiter.waits["sms"] != view.Audience.SMS || r.limiter.waits["email"] != view.Audience.Email {
		t.Fatalf("waits: %v, audience: %+v", r.limiter.waits, view.Audience)
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
	if _, err := r.worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.sender.calls) != 0 {
		t.Fatalf("calls: %d", len(r.sender.calls))
	}
	if got := r.statuses(t); got["RETRYING"] != queued || got["SENDING"] != 0 {
		t.Fatalf("statuses: %v", got)
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
