package delivery_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
)

var refused = delivery.Result{StatusCode: http.StatusUnauthorized}

// smsRefused refuses every SMS with a 401 until ok says otherwise.
func smsRefused(ok func() bool) func(int, delivery.Message) (delivery.Result, error) {
	return func(_ int, message delivery.Message) (delivery.Result, error) {
		if message.Channel == "sms" && !ok() {
			return refused, nil
		}
		return accepted, nil
	}
}

// alertEvents counts a campaign's alert events by kind, channel and whether they opened.
func (r *rig) alertEvents(t *testing.T) map[string]int {
	t.Helper()
	rows, err := r.pool.Query(context.Background(), `
		SELECT event_type || ' ' || (details->>'channel') || ' ' || (details->>'alert'), count(*)
		FROM audit_events WHERE campaign_id = $1 AND event_type LIKE 'campaign.alert_%' GROUP BY 1`, r.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	events := map[string]int{}
	for rows.Next() {
		var (
			key   string
			count int
		)
		if err := rows.Scan(&key, &count); err != nil {
			t.Fatal(err)
		}
		events[key] = count
	}
	return events
}

func (r *rig) channelCounts(t *testing.T, channel string) (statuses map[string]int, attempts int) {
	t.Helper()
	rows, err := r.pool.Query(context.Background(), `SELECT status, count(*), sum(attempts) FROM campaign_deliveries WHERE campaign_id = $1 AND channel = $2 GROUP BY 1`, r.id, channel)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	statuses = map[string]int{}
	for rows.Next() {
		var (
			status   string
			count, n int
		)
		if err := rows.Scan(&status, &count, &n); err != nil {
			t.Fatal(err)
		}
		statuses[status], attempts = count, attempts+n
	}
	return statuses, attempts
}

// A 401 refuses the credentials, not the message: nothing fails, no attempt is used,
// the channel is held at once and the alert goes out at once, while the healthy
// channel finishes. The probe that gets through is sent, once, and opens the channel.
func TestRefusedCredentialsHoldTheChannelUntilAProbeGetsThrough(t *testing.T) {
	t.Parallel()
	var (
		mu sync.Mutex
		ok bool
	)
	r := newRig(t, smsRefused(func() bool { mu.Lock(); defer mu.Unlock(); return ok }))
	ctx := context.Background()
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM campaign_deliveries WHERE campaign_id = $1 AND channel = 'sms'`, r.id).Scan(&total); err != nil {
		t.Fatal(err)
	}
	r.drain(t)

	statuses, attempts := r.channelCounts(t, "sms")
	if statuses["FAILED"] != 0 || statuses["SENT"] != 0 || attempts != 0 || statuses["RETRYING"] == 0 {
		t.Fatalf("SMS after refused credentials: %v, %d attempts", statuses, attempts)
	}
	if email, _ := r.channelCounts(t, "email"); email["QUEUED"]+email["RETRYING"] != 0 || email["SENT"] == 0 {
		t.Fatalf("the healthy channel did not finish: %v", email)
	}
	if !r.limiter.locked("sms") || r.limiter.locked("email") {
		t.Fatal("only SMS should be held")
	}
	if got := r.alertEvents(t); got["campaign.alert_opened sms credentials_refused"] != 1 || len(got) != 1 {
		t.Fatalf("alerts: %v", got)
	}
	refusedCalls := len(r.sender.calls)

	// Nothing goes out on SMS until the probe interval ends.
	if _, err := r.pool.Exec(ctx, `UPDATE campaign_deliveries SET next_attempt_at = now() WHERE campaign_id = $1 AND status = 'RETRYING'`, r.id); err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.worker.ProcessBatch(ctx); err != nil || claimed != 0 {
		t.Fatalf("claimed %d while held: %v", claimed, err)
	}

	// A probe refused again keeps the hold and costs nothing.
	r.limiter.letProbe("sms")
	if claimed, err := r.worker.ProcessBatch(ctx); err != nil || claimed != 1 {
		t.Fatalf("probe: claimed %d, %v", claimed, err)
	}
	if _, attempts := r.channelCounts(t, "sms"); attempts != 0 || !r.limiter.locked("sms") {
		t.Fatalf("a refused probe used an attempt (%d) or opened the channel", attempts)
	}

	mu.Lock()
	ok = true
	mu.Unlock()
	r.limiter.letProbe("sms")
	if claimed, err := r.worker.ProcessBatch(ctx); err != nil || claimed != 1 {
		t.Fatalf("probe: claimed %d, %v", claimed, err)
	}
	if r.limiter.locked("sms") {
		t.Fatal("an accepted probe did not open the channel")
	}
	if statuses, _ := r.channelCounts(t, "sms"); statuses["SENT"] != 1 {
		t.Fatalf("the accepted probe was not recorded as sent: %v", statuses)
	}
	if got := r.alertEvents(t); got["campaign.alert_closed sms credentials_refused"] != 1 {
		t.Fatalf("alerts after the probe got through: %v", got)
	}

	if _, err := r.pool.Exec(ctx, `UPDATE campaign_deliveries SET next_attempt_at = now() WHERE campaign_id = $1 AND status = 'RETRYING'`, r.id); err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	statuses, attempts = r.channelCounts(t, "sms")
	if statuses["SENT"] == 0 || statuses["SENT"] != attempts || statuses["SENT"]+statuses["SKIPPED"] != total {
		t.Fatalf("SMS at the end: %v with %d attempts, want every one sent on its first counted attempt", statuses, attempts)
	}
	sent := map[string]int{}
	for _, call := range r.sender.calls[refusedCalls:] {
		if call.Channel == "sms" {
			sent[call.IdempotencyKey]++
		}
	}
	for key, n := range sent {
		if n > 2 { // the refused probe and the accepted call at most
			t.Fatalf("%s was called %d times after the hold", key, n)
		}
	}
}

// A breaker probe refused with a 401 says nothing about the provider's health. It is
// handed back, so the breaker stays open until a probe is accepted, and the channel
// still sends once the key works again.
func TestABreakerProbeRefusedWithA401IsNotLost(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		phase = "down"
	)
	r := newRig(t, func(_ int, message delivery.Message) (delivery.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case message.Channel != "sms" || phase == "up":
			return accepted, nil
		case phase == "down":
			return delivery.Result{StatusCode: http.StatusBadGateway}, nil
		}
		return refused, nil
	})
	set := func(next string) { mu.Lock(); phase = next; mu.Unlock() }
	queued := r.queuedCount(t)
	worker := breakerWorker(r)
	stop := runWorker(t, worker)
	defer stop()

	waitFor(t, "SMS breaker to trip", func() bool { return testutil.ToFloat64(r.metrics.DeliveryCircuitTrips.WithLabelValues("sms")) > 0 })
	set("refused")
	waitFor(t, "a probe to be refused", func() bool { return r.limiter.locked("sms") })
	if open := testutil.ToFloat64(r.metrics.DeliveryCircuitOpen.WithLabelValues("sms")); open != 1 {
		t.Fatalf("a refused probe closed the breaker (open = %v)", open)
	}
	set("up")
	r.limiter.letProbe("sms")
	waitFor(t, "every message to be sent", func() bool { return r.statuses(t)["SENT"] == queued })
	if open := testutil.ToFloat64(r.metrics.DeliveryCircuitOpen.WithLabelValues("sms")); open != 0 {
		t.Fatalf("the breaker stayed open after an accepted probe (open = %v)", open)
	}
}

// breakerWorker trips after a few failures and probes again within milliseconds. One
// accepted probe closes it.
func breakerWorker(r *rig) *delivery.Worker {
	return delivery.New(r.pool, r.sender, r.limiter, r.metrics, noop.NewTracerProvider().Tracer("test"), delivery.Config{
		BatchSize: 50, Concurrency: 4, Lease: time.Minute, MaxAttempts: 1000, SendTimeout: time.Second,
		Backoff:    func(int) time.Duration { return 0 },
		AlertAfter: 10 * time.Minute,
		Breaker:    delivery.BreakerConfig{Window: 10 * time.Second, MinCalls: 3, FailureRatio: 0.5, FirstWait: 10 * time.Millisecond, MaxWait: 40 * time.Millisecond, Probes: 1},
	})
}

// runWorker runs a worker until the returned stop is called.
func runWorker(t *testing.T, worker *delivery.Worker) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	return func() {
		cancel()
		if err := <-stopped; err != nil {
			t.Errorf("run: %v", err)
		}
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// callsAgo records n calls on the campaign's channel with outcome, at ago before now.
func (r *rig) callsAgo(t *testing.T, channel, outcome string, n int, ago time.Duration) {
	t.Helper()
	_, err := r.pool.Exec(context.Background(), `
		INSERT INTO delivery_attempts (delivery_id, campaign_id, run_id, channel, attempt, outcome, occurred_at)
		SELECT d.id, d.campaign_id, d.run_id, d.channel, 1, $3, now() - make_interval(secs => $5)
		FROM (SELECT id, campaign_id, run_id, channel FROM campaign_deliveries WHERE campaign_id = $1 AND channel = $2 LIMIT 1) d,
		     generate_series(1, $4)`, r.id, channel, outcome, n, ago.Seconds())
	if err != nil {
		t.Fatal(err)
	}
}

func (r *rig) checkAlerts(t *testing.T) {
	t.Helper()
	if err := r.worker.CheckAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A provider that breaks after a healthy stretch raises the alert once its last calls
// mostly fail, however many calls went through before.
func TestFailuresAfterAHealthyStretchRaiseTheAlert(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	r.callsAgo(t, "sms", "accepted", 500, 5*time.Minute)
	// 9 of the last 20 failed: not most of them.
	r.callsAgo(t, "sms", "server_error", 9, 10*time.Second)
	r.checkAlerts(t)
	if got := r.alertEvents(t); got["campaign.alert_opened sms error_rate"] != 0 {
		t.Fatalf("9 of 20 opened the alert: %v", got)
	}
	r.callsAgo(t, "sms", "server_error", 1, 5*time.Second)
	r.checkAlerts(t)
	if got := r.alertEvents(t); got["campaign.alert_opened sms error_rate"] != 1 {
		t.Fatalf("the healthy stretch hid the failures: %v", got)
	}
}

// Each kind of alert opens and closes on its own, and workers checking at the same
// moment write each change once.
// A worker that finds an alert being written waits for that write, then sees it and
// does not write it again.
func TestAnAlertBeingWrittenIsWaitedForAndNotWrittenTwice(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	ctx := context.Background()
	r.callsAgo(t, "sms", "server_error", 15, 3*time.Minute)
	r.callsAgo(t, "sms", "accepted", 10, 3*time.Minute)

	// Another worker is in the middle of opening the same alert.
	other, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := other.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "alert:"+r.id+":sms:error_rate"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(ctx, `INSERT INTO audit_events (campaign_id, event_type, details) VALUES ($1, 'campaign.alert_opened', '{"channel":"sms","alert":"error_rate"}')`, r.id); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- r.worker.CheckAlerts(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("the check did not wait for the alert being written: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := other.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := r.alertEvents(t); len(got) != 1 || got["campaign.alert_opened sms error_rate"] != 1 {
		t.Fatalf("alerts: %v", got)
	}
}

func TestAlertKindsOpenAndCloseOnTheirOwn(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	ctx := context.Background()
	r.callsAgo(t, "sms", "server_error", 15, 3*time.Minute)
	r.callsAgo(t, "sms", "accepted", 10, 3*time.Minute)
	if _, err := r.limiter.LockCredentials(ctx, "sms"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if err := r.worker.CheckAlerts(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	want := map[string]int{"campaign.alert_opened sms error_rate": 1, "campaign.alert_opened sms credentials_refused": 1}
	if got := r.alertEvents(t); len(got) != len(want) || got["campaign.alert_opened sms error_rate"] != 1 || got["campaign.alert_opened sms credentials_refused"] != 1 {
		t.Fatalf("alerts: %v, want %v", got, want)
	}
	live, _, err := r.service.Live(ctx, r.id)
	if err != nil || len(live.Alerts) != 2 {
		t.Fatalf("live alerts: %+v, %v", live.Alerts, err)
	}

	// The credentials are accepted again; most calls still fail.
	if opened, err := r.limiter.UnlockCredentials(ctx, "sms", 1); err != nil || !opened {
		t.Fatalf("unlock: %v, %v", opened, err)
	}
	r.checkAlerts(t)
	got := r.alertEvents(t)
	if got["campaign.alert_closed sms credentials_refused"] != 1 || got["campaign.alert_closed sms error_rate"] != 0 {
		t.Fatalf("one clearing closed the other: %v", got)
	}

	// A few good calls in the last minute are not a recovery; twenty are.
	r.callsAgo(t, "sms", "accepted", 5, 10*time.Second)
	r.checkAlerts(t)
	if got := r.alertEvents(t); got["campaign.alert_closed sms error_rate"] != 0 {
		t.Fatalf("five calls cleared the error rate: %v", got)
	}
	r.callsAgo(t, "sms", "accepted", 20, 10*time.Second)
	r.checkAlerts(t)
	if got := r.alertEvents(t); got["campaign.alert_closed sms error_rate"] != 1 {
		t.Fatalf("a recovered channel kept its alert: %v", got)
	}
	live, _, err = r.service.Live(ctx, r.id)
	if err != nil || len(live.Alerts) != 0 {
		t.Fatalf("live alerts after recovery: %+v, %v", live.Alerts, err)
	}

	notes, err := r.service.Notifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, note := range notes {
		kinds[note.Type]++
	}
	if kinds["campaign.alert_opened"] != 0 || kinds["campaign.alert_closed"] != 2 {
		t.Fatalf("notifications: %v, want the two recoveries and no alert still showing", kinds)
	}
}

// Retries waiting on a channel that accepted nothing for the whole span open an
// alert even while the other channel keeps the campaign moving. One message that gets
// through does not clear it; the channel's work being done does.
func TestAChannelThatAcceptsNothingIsReportedWhileTheOtherMoves(t *testing.T) {
	t.Parallel()
	r := newRig(t, always(accepted, nil))
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `UPDATE campaign_deliveries SET status = 'RETRYING', last_error = 'provider_unreachable' WHERE campaign_id = $1 AND channel = 'sms' AND status = 'QUEUED'`, r.id); err != nil {
		t.Fatal(err)
	}
	r.callsAgo(t, "email", "accepted", 50, time.Minute)
	r.checkAlerts(t)
	if got := r.alertEvents(t); len(got) != 0 {
		t.Fatalf("a campaign that just started was reported: %v", got)
	}

	if _, err := r.pool.Exec(ctx, `UPDATE campaigns SET status_since = now() - interval '11 minutes' WHERE id = $1`, r.id); err != nil {
		t.Fatal(err)
	}
	r.checkAlerts(t)
	if got := r.alertEvents(t); len(got) != 1 || got["campaign.alert_opened sms no_progress"] != 1 {
		t.Fatalf("alerts: %v", got)
	}

	r.callsAgo(t, "sms", "accepted", 1, time.Second)
	r.checkAlerts(t)
	if got := r.alertEvents(t); got["campaign.alert_closed sms no_progress"] != 0 {
		t.Fatalf("one accepted call cleared it: %v", got)
	}

	if _, err := r.pool.Exec(ctx, `UPDATE campaign_deliveries SET status = 'SENT' WHERE campaign_id = $1 AND channel = 'sms' AND status = 'RETRYING'`, r.id); err != nil {
		t.Fatal(err)
	}
	r.checkAlerts(t)
	if got := r.alertEvents(t); got["campaign.alert_closed sms no_progress"] != 1 {
		t.Fatalf("a channel with nothing left kept its alert: %v", got)
	}
}
