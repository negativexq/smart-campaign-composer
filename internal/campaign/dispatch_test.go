package campaign_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/schedule"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

func readyCampaign(t *testing.T) (*harness, string) {
	t.Helper()
	h, _, id := newContentHarness(t, step{output: validContent})
	if _, _, err := run(t, h, id, campaign.GenerateInput{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return h, id
}

func deliveryRows(t *testing.T, h *harness, id string) map[string]string {
	t.Helper()
	rows, err := h.pool.Query(context.Background(), `SELECT user_id::text || ':' || channel, status FROM campaign_deliveries WHERE campaign_id = $1`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := map[string]string{}
	for rows.Next() {
		var key, status string
		if err := rows.Scan(&key, &status); err != nil {
			t.Fatal(err)
		}
		found[key] = status
	}
	return found
}

func TestApprovalFreezesTheVersion(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	ctx := context.Background()

	view, err := h.service.Approve(ctx, id)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if view.Status != "APPROVED" || view.ApprovedAt.IsZero() {
		t.Fatalf("view: %+v", view)
	}
	if got := h.auditEvents(t, id); got[len(got)-1] != "campaign.approved" {
		t.Fatalf("audit: %v", got)
	}

	if _, err := h.service.SaveContent(ctx, id, content.Content{SMS: "a", EmailSubject: "b", EmailBody: "c"}); !errors.Is(err, campaign.ErrNotEditable) {
		t.Errorf("edit after approval: %v", err)
	}
	if _, err := h.service.StartGeneration(ctx, id, campaign.GenerateInput{}); !errors.Is(err, campaign.ErrNotEditable) {
		t.Errorf("generate after approval: %v", err)
	}
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); !errors.Is(err, campaign.ErrNotEditable) {
		t.Errorf("propose after approval: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `UPDATE campaign_versions SET content = '{}' WHERE campaign_id = $1`, id); err == nil {
		t.Error("the database itself must refuse changes to an approved version")
	}
}

func TestApprovingTwiceIsHarmless(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	before := len(h.auditEvents(t, id))
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("second approve: %v", err)
	}
	if len(h.auditEvents(t, id)) != before {
		t.Fatal("a double click must not add a second approval")
	}
}

func TestOnlyAReviewedCampaignCanBeApproved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("draft without content", func(t *testing.T) {
		t.Parallel()
		h, _, id := newContentHarness(t, step{output: validContent})
		if _, err := h.service.Approve(ctx, id); !errors.Is(err, campaign.ErrNotReviewable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		h, _, _ := newContentHarness(t, step{output: validContent})
		if _, err := h.service.Approve(ctx, "nope"); !errors.Is(err, campaign.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("audience no longer reachable", func(t *testing.T) {
		t.Parallel()
		h, id := readyCampaign(t)
		if _, err := h.pool.Exec(ctx, `UPDATE consents SET allowed = false`); err != nil {
			t.Fatal(err)
		}
		if _, err := h.service.Approve(ctx, id); !errors.Is(err, campaign.ErrAudienceBlocked) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("message outgrew the limit because a longer name appeared", func(t *testing.T) {
		t.Parallel()
		h, id := readyCampaign(t)
		edited := content.Content{SMS: "{{first_name}}" + strings.Repeat("x", 130), EmailSubject: "Hello", EmailBody: "Body"}
		if _, err := h.service.SaveContent(ctx, id, edited); err != nil {
			t.Fatalf("save: %v", err)
		}
		if _, err := h.pool.Exec(ctx, `UPDATE users SET first_name = $1 WHERE id = $2`, strings.Repeat("n", 40), seed.FixtureTurkishName); err != nil {
			t.Fatal(err)
		}
		var invalid *content.ValidationError
		if _, err := h.service.Approve(ctx, id); !errors.As(err, &invalid) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestDispatchNeedsApproval(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	if _, err := h.service.Dispatch(context.Background(), id); !errors.Is(err, campaign.ErrNotApproved) {
		t.Fatalf("got %v", err)
	}
	if rows := deliveryRows(t, h, id); len(rows) != 0 {
		t.Fatalf("rows: %v", rows)
	}
}

func TestDispatchQueuesConsentedPeopleAndRecordsTheRest(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	view, err := h.service.Dispatch(ctx, id)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if view.Status != "DISPATCHING" {
		t.Fatalf("status: %s", view.Status)
	}

	rows := deliveryRows(t, h, id)
	for key, want := range map[string]string{
		"1:sms":   "QUEUED",
		"1:email": "QUEUED",
		"8:sms":   "QUEUED",  // SMS only
		"8:email": "SKIPPED", //
		"9:sms":   "SKIPPED", // email only
		"9:email": "QUEUED",
		"10:sms":  "SKIPPED", // no consent at all
	} {
		if rows[key] != want {
			t.Errorf("%s: got %q, want %q", key, rows[key], want)
		}
	}
	if _, reached := rows["2:sms"]; reached {
		t.Error("someone who purchased after the cart is outside the audience")
	}

	queued := 0
	for _, status := range rows {
		if status == "QUEUED" {
			queued++
		}
	}
	if want := view.Audience.SMS + view.Audience.Email; queued != want {
		t.Fatalf("queued %d, reviewed audience says %d", queued, want)
	}
	if got := h.auditEvents(t, id); got[len(got)-1] != "campaign.dispatched" {
		t.Fatalf("audit: %v", got)
	}
}

func TestDispatchingAgainCreatesNothingNew(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	before := deliveryRows(t, h, id)
	audit := len(h.auditEvents(t, id))
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if after := deliveryRows(t, h, id); len(after) != len(before) {
		t.Fatalf("rows: %d then %d", len(before), len(after))
	}
	if len(h.auditEvents(t, id)) != audit {
		t.Fatal("repeating dispatch must not be recorded as a new one")
	}
}

func TestConcurrentDispatchesCreateOneSet(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	errs := make(chan error, 5)
	for range 5 {
		go func() {
			_, err := h.service.Dispatch(ctx, id)
			errs <- err
		}()
	}
	for range 5 {
		if err := <-errs; err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}
	var audits int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE campaign_id = $1 AND event_type = 'campaign.dispatched'`, id).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("dispatch audits: %d", audits)
	}
}

func TestProgressAndDeliveriesAreReadable(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	progress, err := h.service.Progress(ctx, id)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if progress.Total != 18 || progress.ByStatus["SKIPPED"]+progress.ByStatus["QUEUED"] != 18 {
		t.Fatalf("progress: %+v", progress)
	}
	if progress.ByChannel["sms"]["QUEUED"] == 0 || progress.ByChannel["email"]["QUEUED"] == 0 {
		t.Fatalf("by channel: %+v", progress.ByChannel)
	}

	all, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{})
	if err != nil {
		t.Fatalf("deliveries: %v", err)
	}
	if len(all) != 18 {
		t.Fatalf("rows: %d", len(all))
	}
	skipped, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Status: "SKIPPED", Channel: "sms"})
	if err != nil {
		t.Fatalf("filtered: %v", err)
	}
	for _, row := range skipped {
		if row.Status != "SKIPPED" || row.Channel != "sms" || row.Reason == "" {
			t.Fatalf("row: %+v", row)
		}
	}
	if len(skipped) == 0 {
		t.Fatal("expected skipped rows")
	}
	if _, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Status: "NOPE"}); !errors.Is(err, campaign.ErrUnknownFilter) {
		t.Fatalf("bad filter: %v", err)
	}
}

func nextHour() *time.Time {
	future := referenceTime.Add(time.Hour)
	return &future
}

func approvedCampaign(t *testing.T) (*harness, string) {
	t.Helper()
	h, id := readyCampaign(t)
	if _, err := h.service.Approve(context.Background(), id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return h, id
}

func scheduler(h *harness, now time.Time) *campaign.Scheduler {
	return campaign.NewScheduler(h.pool, noop.NewTracerProvider().Tracer("test"), func() time.Time { return now })
}

func TestSchedulingWaitsForTheTime(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()

	view, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if view.Status != "SCHEDULED" || view.Schedule == nil || !view.Schedule.NextRunAt.Equal(*nextHour()) || view.Schedule.Description == "" {
		t.Fatalf("view: %s %+v", view.Status, view.Schedule)
	}
	if got := h.auditEvents(t, id); got[len(got)-1] != "campaign.scheduled" {
		t.Fatalf("audit: %v", got)
	}

	if ran, err := scheduler(h, referenceTime).RunDue(ctx); err != nil || ran != 0 {
		t.Fatalf("too early: %d %v", ran, err)
	}
	if len(deliveryRows(t, h, id)) != 0 {
		t.Fatal("nothing may be queued before the time")
	}

	if ran, err := scheduler(h, nextHour().Add(time.Second)).RunDue(ctx); err != nil || ran != 1 {
		t.Fatalf("on time: %d %v", ran, err)
	}
	if statusOf(t, h, id) != "DISPATCHING" || len(deliveryRows(t, h, id)) != 18 {
		t.Fatalf("status %s, rows %d", statusOf(t, h, id), len(deliveryRows(t, h, id)))
	}
	if ran, _ := scheduler(h, nextHour().Add(time.Minute)).RunDue(ctx); ran != 0 {
		t.Fatal("a one-time schedule must not run again")
	}
	view, _ = h.service.Get(ctx, id)
	if view.Schedule.NextRunAt != nil || view.Schedule.Runs != 1 {
		t.Fatalf("schedule: %+v", view.Schedule)
	}
}

func TestSchedulingNeedsApprovalAndAValidRule(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	ctx := context.Background()
	spec := schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}
	if _, err := h.service.Schedule(ctx, id, spec); !errors.Is(err, campaign.ErrNotApproved) {
		t.Fatalf("before approval: %v", err)
	}
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatal(err)
	}
	past := referenceTime.Add(-time.Hour)
	var invalid *schedule.InvalidError
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: &past}); !errors.As(err, &invalid) {
		t.Fatalf("past: %v", err)
	}
}

func TestASchedulePendingCannotAlsoBeSentNow(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Dispatch(ctx, id); !errors.Is(err, campaign.ErrAlreadyScheduled) {
		t.Fatalf("got %v", err)
	}
}

func TestChangingAScheduleReplacesIt(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindInterval, EveryHours: 6})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if view.Schedule.Spec.Kind != schedule.KindInterval {
		t.Fatalf("schedule: %+v", view.Schedule)
	}
}

func TestCancellingBeforeTheFirstSendGoesBackToApproved(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}); err != nil {
		t.Fatal(err)
	}
	view, err := h.service.CancelSchedule(ctx, id)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if view.Status != "APPROVED" || view.Schedule != nil {
		t.Fatalf("view: %s %+v", view.Status, view.Schedule)
	}
	if ran, _ := scheduler(h, nextHour().Add(time.Second)).RunDue(ctx); ran != 0 {
		t.Fatal("a cancelled schedule must not run")
	}
}

func TestARepeatingScheduleNeverMessagesAnyoneTwice(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindInterval, EveryHours: 24}); err != nil {
		t.Fatal(err)
	}
	first := referenceTime.Add(24 * time.Hour)
	if ran, err := scheduler(h, first.Add(time.Second)).RunDue(ctx); err != nil || ran != 1 {
		t.Fatalf("first run: %d %v", ran, err)
	}
	if _, err := h.pool.Exec(ctx, `UPDATE campaign_deliveries SET status = 'SENT' WHERE status = 'QUEUED'`); err != nil {
		t.Fatal(err)
	}
	before := deliveryRows(t, h, id)

	// Someone new starts matching: a user from elsewhere moves to Istanbul with a cart.
	if _, err := h.pool.Exec(ctx, `UPDATE users SET city = 'İstanbul' WHERE id = $1`, seed.FixtureOtherCity); err != nil {
		t.Fatal(err)
	}
	second := first.Add(24 * time.Hour)
	if ran, err := scheduler(h, second.Add(time.Second)).RunDue(ctx); err != nil || ran != 1 {
		t.Fatalf("second run: %d %v", ran, err)
	}
	after := deliveryRows(t, h, id)
	for key, status := range before {
		if after[key] != status {
			t.Errorf("%s changed from %s to %s", key, status, after[key])
		}
	}
	if after["5:sms"] != "QUEUED" || after["5:email"] != "QUEUED" || len(after) != len(before)+2 {
		t.Fatalf("only the new person is queued: %v", after)
	}
	view, _ := h.service.Get(ctx, id)
	if view.Schedule.Runs != 2 || view.Schedule.NextRunAt == nil {
		t.Fatalf("schedule: %+v", view.Schedule)
	}
}

func TestSomeoneWhoConsentsLaterIsReachedByALaterRun(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindInterval, EveryHours: 24}); err != nil {
		t.Fatal(err)
	}
	first := referenceTime.Add(24 * time.Hour)
	if _, err := scheduler(h, first.Add(time.Second)).RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if rows := deliveryRows(t, h, id); rows["10:sms"] != "SKIPPED" {
		t.Fatalf("rows: %v", rows)
	}
	if _, err := h.pool.Exec(ctx, `UPDATE consents SET allowed = true WHERE user_id = $1 AND channel = 'sms'`, seed.FixtureNoConsent); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler(h, first.Add(24*time.Hour+time.Second)).RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if rows := deliveryRows(t, h, id); rows["10:sms"] != "QUEUED" {
		t.Fatalf("a skipped person was never messaged, so they can still be: %v", rows)
	}
}

func TestParallelSchedulersRunOnce(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}); err != nil {
		t.Fatal(err)
	}
	results := make(chan int, 4)
	for range 4 {
		go func() {
			ran, err := scheduler(h, nextHour().Add(time.Second)).RunDue(ctx)
			if err != nil {
				t.Errorf("run: %v", err)
			}
			results <- ran
		}()
	}
	total := 0
	for range 4 {
		total += <-results
	}
	if total != 1 {
		t.Fatalf("ran %d times", total)
	}
	var audits int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE campaign_id = $1 AND event_type = 'campaign.run'`, id).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("audits: %d", audits)
	}
}

func TestScheduledCampaignsCountAsActive(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}); err != nil {
		t.Fatal(err)
	}
	active, err := h.service.List(ctx, campaign.ListFilter{Group: "active"})
	if err != nil || len(active) != 1 {
		t.Fatalf("active: %d %v", len(active), err)
	}
}

// Nothing about a schedule lives in the process: a new service and scheduler on the
// same database, as after a restart, must carry on from what is stored.
func TestSchedulesOutliveTheProcessThatMadeThem(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	restarted := campaign.NewService(h.pool, ai.NewMock(), telemetry.NewMetrics(prometheus.NewRegistry()),
		noop.NewTracerProvider().Tracer("test"), func() time.Time { return referenceTime })
	view, err := restarted.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "SCHEDULED" || view.Schedule == nil || view.Schedule.NextRunAt == nil || !view.Schedule.NextRunAt.Equal(*nextHour()) {
		t.Fatalf("after a restart: %s %+v", view.Status, view.Schedule)
	}

	// The time passed while nothing was running: it still goes out, and only once.
	late := scheduler(h, nextHour().Add(6*time.Hour))
	if ran, err := late.RunDue(ctx); err != nil || ran != 1 {
		t.Fatalf("missed run: %d %v", ran, err)
	}
	if ran, err := late.RunDue(ctx); err != nil || ran != 0 {
		t.Fatalf("second pass: %d %v", ran, err)
	}
}

func TestMissedIntervalsBecomeOneRunAndThenStayOnTime(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindInterval, EveryHours: 1}); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	later := referenceTime.Add(10*time.Hour + 30*time.Minute)
	if ran, err := scheduler(h, later).RunDue(ctx); err != nil || ran != 1 {
		t.Fatalf("after downtime: %d %v", ran, err)
	}
	view, err := h.service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Schedule.Runs != 1 || view.Schedule.NextRunAt == nil || !view.Schedule.NextRunAt.After(later) {
		t.Fatalf("schedule: %+v", view.Schedule)
	}
}

func TestAScheduledRunIsTheSystemsOnBehalfOfWhoeverSetItUp(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := campaign.WithActor(context.Background(), "marketer@example.com")
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if ran, err := scheduler(h, nextHour().Add(time.Second)).RunDue(context.Background()); err != nil || ran != 1 {
		t.Fatalf("run: %d %v", ran, err)
	}
	entries, err := h.service.Activity(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var setUp, run *campaign.ActivityEntry
	for i := range entries {
		switch entries[i].Type {
		case "campaign.scheduled":
			setUp = &entries[i]
		case "campaign.run":
			run = &entries[i]
		}
	}
	if setUp == nil || setUp.ActorType != "user" || setUp.Actor == nil || *setUp.Actor != "marketer@example.com" {
		t.Fatalf("set up: %+v", setUp)
	}
	if run == nil || run.ActorType != "system" || run.Actor != nil || !strings.Contains(string(run.Details), "marketer@example.com") {
		t.Fatalf("run: %+v", run)
	}
}


func TestAScheduledRunStaysOnTheCampaignsChannels(t *testing.T) {
	t.Parallel()
	h, id := approvedCampaign(t)
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET channels = '{email}' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Schedule(ctx, id, schedule.Spec{Kind: schedule.KindOnce, At: nextHour()}); err != nil {
		t.Fatal(err)
	}
	if ran, err := scheduler(h, nextHour().Add(time.Second)).RunDue(ctx); err != nil || ran != 1 {
		t.Fatalf("run: %d %v", ran, err)
	}
	rows := deliveryRows(t, h, id)
	if len(rows) == 0 {
		t.Fatal("nothing was queued")
	}
	for key := range rows {
		if !strings.HasSuffix(key, ":email") {
			t.Fatalf("delivery outside the campaign's channels: %s", key)
		}
	}
}
