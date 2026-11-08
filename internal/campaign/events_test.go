package campaign_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

func keyOf(t *testing.T, h *harness, deliveryID string) string {
	t.Helper()
	var key string
	if err := h.pool.QueryRow(context.Background(), `SELECT idempotency_key FROM campaign_deliveries WHERE id = $1`, deliveryID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestAnOpenedEventReachesTheReportAndTheDelivery(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 3, 10, 5, 0, 0, time.UTC)

	if err := h.service.RecordDeliveryEvent(ctx, keyOf(t, h, named["accepted"]), "opened", at); err != nil {
		t.Fatalf("record: %v", err)
	}
	// Webhooks repeat: a later report must not move the time or count twice.
	if err := h.service.RecordDeliveryEvent(ctx, keyOf(t, h, named["accepted"]), "opened", at.Add(time.Hour)); err != nil {
		t.Fatalf("repeat: %v", err)
	}

	report, err := h.service.Report(ctx, id)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	accepted, opened := 0, 0
	for _, channel := range report.Engagement {
		accepted += channel.Accepted
		opened += channel.Opened
	}
	if accepted != 1 || opened != 1 {
		t.Fatalf("engagement: %+v", report.Engagement)
	}

	rows, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Opened: "yes"})
	if err != nil {
		t.Fatalf("opened: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != named["accepted"] || rows[0].OpenedAt == nil || !rows[0].OpenedAt.Equal(at) {
		t.Fatalf("rows: %+v", rows)
	}
	unopened, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Opened: "no"})
	if err != nil {
		t.Fatalf("not opened: %v", err)
	}
	for _, row := range unopened {
		if row.ID == named["accepted"] || row.OpenedAt != nil {
			t.Fatalf("listed as not opened: %+v", row)
		}
	}
}

func TestAnOpenedEventDoesNotChangeTheDeliveryResult(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	ctx := context.Background()
	// A message with an unknown outcome did arrive if someone opened it, but the
	// record of what our call returned stays as it was.
	if err := h.service.RecordDeliveryEvent(ctx, keyOf(t, h, named["unknown"]), "opened", time.Now()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := deliveryRowsByID(t, h, id)[named["unknown"]]; got != "UNKNOWN" {
		t.Fatalf("status: %s", got)
	}
}

func TestEventsThatMakeNoSenseAreRefused(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	ctx := context.Background()
	if err := h.service.RecordDeliveryEvent(ctx, "no-such-key", "opened", time.Now()); !errors.Is(err, campaign.ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
	if err := h.service.RecordDeliveryEvent(ctx, keyOf(t, h, named["accepted"]), "exploded", time.Now()); !errors.Is(err, campaign.ErrUnknownEvent) {
		t.Fatalf("unknown event: %v", err)
	}
	if _, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Opened: "maybe"}); !errors.Is(err, campaign.ErrUnknownFilter) {
		t.Fatalf("bad filter: %v", err)
	}
	// The total counts what the filter keeps, whatever the page size.
	everyone := deliveryRowsByID(t, h, id)
	if len(everyone) == 0 {
		t.Fatal("no deliveries to count")
	}
	// Nothing has been reported opened here.
	for filter, want := range map[string]int{"": len(everyone), "yes": 0, "no": len(everyone)} {
		total, err := h.service.CountDeliveries(ctx, id, campaign.DeliveryFilter{Opened: filter, Limit: 1})
		if err != nil || total != want {
			t.Fatalf("opened %q: total %d, want %d, %v", filter, total, want, err)
		}
	}
	if _, err := h.service.CountDeliveries(ctx, id, campaign.DeliveryFilter{Opened: "maybe"}); !errors.Is(err, campaign.ErrUnknownFilter) {
		t.Fatalf("bad filter on the total: %v", err)
	}
}

func deliveryRowsByID(t *testing.T, h *harness, id string) map[string]string {
	t.Helper()
	rows, err := h.service.Deliveries(context.Background(), id, campaign.DeliveryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, row := range rows {
		found[row.ID] = row.Status
	}
	return found
}

func lifecycleOf(t *testing.T, h *harness, id string) campaign.ReportLifecycle {
	t.Helper()
	report, err := h.service.Report(context.Background(), id)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	var total campaign.ReportLifecycle
	for _, channel := range report.Lifecycle {
		total.Planned += channel.Planned
		total.Eligible += channel.Eligible
		total.Called += channel.Called
		total.Accepted += channel.Accepted
		total.Delivered += channel.Delivered
		total.Bounced += channel.Bounced
		total.Waiting += channel.Waiting
		total.ArrivedUnknown += channel.ArrivedUnknown
	}
	return total
}

func TestAnAcceptedMessageWaitsThenArrivesOrBounces(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	ctx := context.Background()
	if got := lifecycleOf(t, h, id); got.Accepted != 1 || got.Waiting != 1 || got.Delivered != 0 || got.Called != 3 || got.Planned < 3 {
		t.Fatalf("before a report: %+v", got)
	}
	waiting, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Arrival: "waiting"})
	if err != nil || len(waiting) != 1 || waiting[0].ID != named["accepted"] {
		t.Fatalf("waiting: %+v %v", waiting, err)
	}

	at := time.Date(2026, 11, 3, 10, 5, 0, 0, time.UTC)
	if err := h.service.RecordDeliveryEvent(ctx, keyOf(t, h, named["accepted"]), "delivered", at); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := lifecycleOf(t, h, id); got.Delivered != 1 || got.Waiting != 0 || got.Bounced != 0 {
		t.Fatalf("delivered: %+v", got)
	}
	rows, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Arrival: "delivered"})
	if err != nil || len(rows) != 1 || rows[0].DeliveredAt == nil || !rows[0].DeliveredAt.Equal(at) || rows[0].Status != "SENT" {
		t.Fatalf("rows: %+v %v", rows, err)
	}

	// A bounce after an arrival report is the more specific news.
	if err := h.service.RecordDeliveryEvent(ctx, keyOf(t, h, named["accepted"]), "bounced", at.Add(time.Minute)); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := lifecycleOf(t, h, id); got.Delivered != 0 || got.Bounced != 1 || got.Waiting != 0 {
		t.Fatalf("bounced: %+v", got)
	}
	rows, err = h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Arrival: "bounced"})
	if err != nil || len(rows) != 1 || rows[0].BouncedAt == nil || rows[0].DeliveredAt != nil {
		t.Fatalf("bounced rows: %+v %v", rows, err)
	}
}

func TestAnOpenProvesArrivalEvenWithoutADeliveredReport(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	if err := h.service.RecordDeliveryEvent(context.Background(), keyOf(t, h, named["accepted"]), "opened", time.Now()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := lifecycleOf(t, h, id); got.Delivered != 1 || got.Waiting != 0 {
		t.Fatalf("lifecycle: %+v", got)
	}
}

func TestAnArrivalReportDoesNotChangeAnUnknownResult(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	ctx := context.Background()
	if err := h.service.RecordDeliveryEvent(ctx, keyOf(t, h, named["unknown"]), "delivered", time.Now()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := lifecycleOf(t, h, id); got.ArrivedUnknown != 1 || got.Delivered != 0 || got.Accepted != 1 {
		t.Fatalf("lifecycle: %+v", got)
	}
	rows, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Status: "UNKNOWN", Arrival: "delivered"})
	if err != nil || len(rows) != 1 || rows[0].Status != "UNKNOWN" {
		t.Fatalf("rows: %+v %v", rows, err)
	}
	if _, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Arrival: "maybe"}); !errors.Is(err, campaign.ErrUnknownFilter) {
		t.Fatalf("filter: %v", err)
	}
}

func TestDeliveryMonitorListsWhatNeedsWatchingAcrossCampaigns(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	ctx := context.Background()
	failed := named["accepted"]
	if _, err := h.pool.Exec(ctx, `UPDATE campaign_deliveries SET status = 'FAILED', last_error = 'timeout' WHERE id = $1`, failed); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO delivery_attempts (delivery_id, campaign_id, run_id, channel, attempt, outcome)
		SELECT id, campaign_id, run_id, channel, 1, 'timeout' FROM campaign_deliveries WHERE id = $1`, failed); err != nil {
		t.Fatal(err)
	}

	everything, err := h.service.DeliveryMonitor(ctx, campaign.MonitorFilter{})
	if err != nil {
		t.Fatalf("monitor: %v", err)
	}
	if everything.Counts["FAILED"] < 1 || everything.Total != len(everything.Deliveries) {
		t.Fatalf("monitor: %+v", everything)
	}
	for _, status := range []string{"QUEUED", "SENDING", "RETRYING", "UNKNOWN", "FAILED"} {
		if _, known := everything.Counts[status]; !known {
			t.Fatalf("%s missing from %v", status, everything.Counts)
		}
	}
	for _, row := range everything.Deliveries {
		if row.Status == "SENT" || row.Status == "SKIPPED" {
			t.Fatalf("listed a delivery nobody needs to watch: %+v", row)
		}
	}

	byOutcome, err := h.service.DeliveryMonitor(ctx, campaign.MonitorFilter{Status: "FAILED", Outcome: "timeout", WithinHours: 1})
	if err != nil {
		t.Fatalf("filtered: %v", err)
	}
	if byOutcome.Total != 1 || len(byOutcome.Deliveries) != 1 {
		t.Fatalf("filtered: %+v", byOutcome)
	}
	found := byOutcome.Deliveries[0]
	if found.ID != failed || found.CampaignID != id || found.CampaignName == "" || found.Reason != "timeout" || found.Calls < 1 {
		t.Fatalf("row: %+v", found)
	}

	// Each breakdown follows the other filters and leaves out its own.
	clean, err := h.service.DeliveryMonitor(ctx, campaign.MonitorFilter{Reason: campaign.ReasonNone})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range clean.Deliveries {
		if row.Reason != "" {
			t.Fatalf("no problem yet listed %+v", row)
		}
	}
	sms, err := h.service.DeliveryMonitor(ctx, campaign.MonitorFilter{Channel: "sms", Reason: "timeout"})
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	if sms.Channels["email"] < 1 || sms.Channels["sms"] != 0 || sms.Total != 0 {
		t.Fatalf("channels should ignore the channel filter: %+v", sms)
	}
	reasons := map[string]int{}
	sum := 0
	for _, reason := range everything.Reasons {
		reasons[reason.Reason] = reason.Count
		sum += reason.Count
	}
	if reasons["timeout"] < 1 || sum != everything.Total || everything.Channels["sms"]+everything.Channels["email"] != everything.Total {
		t.Fatalf("breakdowns do not add up: %+v", everything)
	}

	// An older problem falls outside a recent window, and the counts follow the filter.
	if _, err := h.pool.Exec(ctx, `UPDATE campaign_deliveries SET updated_at = '2000-01-01' WHERE id = $1`, failed); err != nil {
		t.Fatal(err)
	}
	recent, err := h.service.DeliveryMonitor(ctx, campaign.MonitorFilter{Status: "FAILED", WithinHours: 1})
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if recent.Counts["FAILED"] != everything.Counts["FAILED"]-1 || recent.Total != recent.Counts["FAILED"] {
		t.Fatalf("recent: %+v, before %+v", recent.Counts, everything.Counts)
	}
	for _, row := range recent.Deliveries {
		if row.ID == failed {
			t.Fatalf("listed an older problem: %+v", row)
		}
	}

	if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET status = 'PAUSED' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	paused, err := h.service.DeliveryMonitor(ctx, campaign.MonitorFilter{Limit: 1})
	if err != nil || len(paused.Paused) != 1 || paused.Paused[0].ID != id {
		t.Fatalf("paused: %+v %v", paused.Paused, err)
	}

	for _, bad := range []campaign.MonitorFilter{{Status: "SENT"}, {Channel: "fax"}, {Outcome: "nope"}, {WithinHours: -1}} {
		if _, err := h.service.DeliveryMonitor(ctx, bad); !errors.Is(err, campaign.ErrUnknownFilter) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
}

func TestSavedAudienceKeepsTheCampaignsRuleAndCountsApart(t *testing.T) {
	t.Parallel()
	h, id, _ := sentCampaign(t)
	ctx := context.Background()
	view, err := h.service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := h.service.SaveSegment(ctx, id, "  Istanbul cart abandoners ")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Name != "Istanbul cart abandoners" || len(saved.Conditions) != len(view.Conditions) || saved.SourceCampaignID == nil || *saved.SourceCampaignID != id || saved.SourceCampaignName == nil {
		t.Fatalf("saved: %+v", saved)
	}
	if _, err := h.service.SaveSegment(ctx, id, "ISTANBUL CART ABANDONERS"); !errors.Is(err, campaign.ErrSegmentNameTaken) {
		t.Fatalf("same name: %v", err)
	}
	if _, err := h.service.SaveSegment(ctx, id, "  "); !errors.Is(err, campaign.ErrSegmentName) {
		t.Fatalf("no name: %v", err)
	}
	if _, err := h.service.SaveSegment(ctx, "00000000-0000-0000-0000-000000000000", "x"); !errors.Is(err, campaign.ErrNotFound) {
		t.Fatalf("no campaign: %v", err)
	}

	// Its counts are from when it was last asked, until someone asks again.
	people := saved.Audience.Total
	if _, err := h.pool.Exec(ctx, `UPDATE saved_segments SET total = total + 5 WHERE id = $1`, saved.ID); err != nil {
		t.Fatal(err)
	}
	list, err := h.service.SavedSegments(ctx)
	if err != nil || len(list) != 1 || list[0].Audience.Total != people+5 {
		t.Fatalf("list: %+v %v", list, err)
	}
	recounted, err := h.service.RecountSegment(ctx, saved.ID)
	if err != nil || recounted.Audience.Total != people || recounted.CountedAt.Before(saved.CountedAt) {
		t.Fatalf("recount: %+v %v", recounted, err)
	}

	// Its campaign can go away without it.
	if err := h.service.DeleteSegment(ctx, saved.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, missing := range []string{saved.ID, "nope"} {
		if _, err := h.service.RecountSegment(ctx, missing); !errors.Is(err, campaign.ErrSegmentNotFound) {
			t.Fatalf("recount missing: %v", err)
		}
		if err := h.service.DeleteSegment(ctx, missing); !errors.Is(err, campaign.ErrSegmentNotFound) {
			t.Fatalf("delete missing: %v", err)
		}
	}
}

// The saved rule is the audience, so a brief about the message alone must not be read
// for one. The harness has no answers left, so any model call here would fail the test.
func TestACampaignFromASavedAudienceKeepsItsRuleWithoutAModel(t *testing.T) {
	t.Parallel()
	h, source, _ := sentCampaign(t)
	ctx := context.Background()
	saved, err := h.service.SaveSegment(ctx, source, "Reusable")
	if err != nil {
		t.Fatal(err)
	}
	quiet := 14
	id, err := h.service.CreateWith(ctx, campaign.NewCampaign{Brief: "Tell them about free shipping.", SavedAudienceID: saved.ID, NotMessagedWithinDays: &quiet})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	view, err := h.service.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Proposal == nil || view.Proposal.SavedAudience != "Reusable" || len(view.Blockers) != 0 || view.Brief != "Tell them about free shipping." {
		t.Fatalf("view: %+v", view)
	}
	if len(view.Conditions) != len(saved.Conditions)+1 || view.Proposal.Segment.NotMessagedWithinDays == nil {
		t.Fatalf("conditions: %+v, saved: %+v", view.Conditions, saved.Conditions)
	}

	// The audience step asks again when it opens, and editing the brief must keep the rule.
	if view, err = h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil || view.Proposal.SavedAudience != "Reusable" {
		t.Fatalf("asked again: %+v %v", view.Proposal, err)
	}
	edited := "Tell them about the sale."
	if view, err = h.service.ProposeSegment(ctx, id, campaign.ProposeInput{Brief: &edited}); err != nil || view.Brief != edited || view.Proposal.SavedAudience != "Reusable" {
		t.Fatalf("edited brief: %+v %v", view, err)
	}

	// The campaign has its own copy.
	if err := h.service.DeleteSegment(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if view, err = h.service.Get(ctx, id); err != nil || view.Proposal.SavedAudience != "Reusable" || len(view.Conditions) == 0 {
		t.Fatalf("after delete: %+v %v", view, err)
	}
	if _, err := h.service.CreateWith(ctx, campaign.NewCampaign{Brief: "Hello", SavedAudienceID: saved.ID}); !errors.Is(err, campaign.ErrSegmentNotFound) {
		t.Fatalf("deleted audience: %v", err)
	}
}

func TestACampaignWithoutARuleCannotBeSavedAsAnAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	id := h.createWithStatus(t, "No rule yet", "DRAFT")
	if _, err := h.service.SaveSegment(context.Background(), id, "Nothing"); !errors.Is(err, campaign.ErrSegmentNotSavable) {
		t.Fatalf("got %v", err)
	}
}

func TestAnalyticsComparesCampaignsInsideTheWindow(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	ctx := context.Background()
	report, err := h.service.Analytics(ctx, 30)
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	planned, accepted := 0, 0
	for _, channel := range report.Channels {
		planned += channel.Planned
		accepted += channel.Accepted
		if channel.Delivered+channel.Bounced+channel.Waiting != channel.Accepted {
			t.Fatalf("arrival does not add up: %+v", channel)
		}
		if channel.Skipped+channel.Accepted+channel.Failed+channel.Unknown+channel.OnTheWay != channel.Planned {
			t.Fatalf("statuses do not add up: %+v", channel)
		}
	}
	rows := deliveryRowsByID(t, h, id)
	if planned != len(rows) || accepted == 0 {
		t.Fatalf("planned %d of %d, accepted %d", planned, len(rows), accepted)
	}
	if len(report.Campaigns) != 1 || report.Campaigns[0].ID != id {
		t.Fatalf("campaigns: %+v", report.Campaigns)
	}

	if _, err := h.pool.Exec(ctx, `
		INSERT INTO delivery_attempts (delivery_id, campaign_id, run_id, channel, attempt, outcome)
		SELECT id, campaign_id, run_id, channel, 90, 'timeout' FROM campaign_deliveries WHERE id = $1`, named["accepted"]); err != nil {
		t.Fatal(err)
	}
	withCall, err := h.service.Analytics(ctx, 7)
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	notAccepted := 0
	for _, day := range withCall.Calls {
		notAccepted += day.NotAccepted
	}
	if notAccepted == 0 {
		t.Fatalf("calls: %+v", withCall.Calls)
	}

	// Older than the window counts for nothing.
	if _, err := h.pool.Exec(ctx, `UPDATE campaign_deliveries SET created_at = '2000-01-01' WHERE campaign_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	old, err := h.service.Analytics(ctx, 30)
	if err != nil || len(old.Channels) != 0 || len(old.Campaigns) != 0 {
		t.Fatalf("old: %+v %v", old, err)
	}
	if _, err := h.service.Analytics(ctx, 8); !errors.Is(err, campaign.ErrUnknownFilter) {
		t.Fatalf("unknown range: %v", err)
	}
}

// Analytics reads every delivery's arrival from its events: a bounce outweighs a
// delivered report, an open alone shows the message arrived, and the time the
// provider gives an event does not decide whether it counts.
func TestAnalyticsCountsArrivalFromEachDeliverysEvents(t *testing.T) {
	t.Parallel()
	h, id, _ := sentCampaign(t)
	ctx := context.Background()

	rows, err := h.pool.Query(ctx, `SELECT id FROM campaign_deliveries WHERE campaign_id = $1 AND status = 'QUEUED' ORDER BY id LIMIT 6`, id)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(ids) < 6 {
		t.Fatalf("queued: %d %v", len(ids), err)
	}
	if _, err := h.pool.Exec(ctx, `UPDATE campaign_deliveries SET status = 'SENT' WHERE id = ANY($1)`, ids); err != nil {
		t.Fatal(err)
	}
	events := []struct {
		delivery int
		kind     string
		at       string
	}{
		{0, "delivered", "2026-11-03T12:01:00Z"},
		{1, "bounced", "2026-11-03T12:01:00Z"},
		{2, "delivered", "2026-11-03T12:01:00Z"},
		{2, "opened", "2026-11-03T12:30:00Z"},
		{3, "opened", "2026-11-03T12:30:00Z"},
		{4, "delivered", "2026-11-03T12:01:00Z"},
		{4, "bounced", "2026-11-03T12:02:00Z"},
		// A provider clock far behind still reports an arrival inside the window.
		{5, "delivered", "2000-01-01T00:00:00Z"},
	}
	for _, e := range events {
		if _, err := h.pool.Exec(ctx, `INSERT INTO delivery_events (delivery_id, campaign_id, event_type, occurred_at) VALUES ($1, $2, $3, $4)`,
			ids[e.delivery], id, e.kind, e.at); err != nil {
			t.Fatal(err)
		}
	}

	report, err := h.service.Analytics(ctx, 30)
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	var sum campaign.ChannelTotals
	for _, channel := range report.Channels {
		sum.Accepted += channel.Accepted
		sum.Delivered += channel.Delivered
		sum.Bounced += channel.Bounced
		sum.Waiting += channel.Waiting
		sum.Opened += channel.Opened
	}
	// sentCampaign accepted one delivery with no events; six more are accepted here.
	want := campaign.ChannelTotals{Accepted: 7, Delivered: 4, Bounced: 2, Waiting: 1, Opened: 2}
	if sum != want {
		t.Fatalf("totals %+v, want %+v", sum, want)
	}
}

// Webhooks repeat and arrive out of order. The report reads the set of events a
// delivery has, not the order they came in, so every order gives the same answer.
func TestReportsThatRepeatOrArriveOutOfOrderGiveTheSameResult(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	type event struct {
		name  string
		after time.Duration
	}
	for name, tt := range map[string]struct {
		events      []event
		delivered   int
		bounced     int
		deliveredAt *time.Time
	}{
		"delivered twice":            {[]event{{"delivered", time.Minute}, {"delivered", time.Hour}}, 1, 0, ptr(base.Add(time.Minute))},
		"bounced twice":              {[]event{{"bounced", time.Minute}, {"bounced", time.Hour}}, 0, 1, nil},
		"bounced, then delivered":    {[]event{{"bounced", 2 * time.Minute}, {"delivered", time.Minute}}, 0, 1, nil},
		"delivered, then bounced":    {[]event{{"delivered", time.Minute}, {"bounced", 2 * time.Minute}}, 0, 1, nil},
		"opened before delivered":    {[]event{{"opened", 5 * time.Minute}, {"delivered", time.Minute}}, 1, 0, ptr(base.Add(time.Minute))},
		"everything twice, reversed": {[]event{{"opened", 5 * time.Minute}, {"delivered", time.Minute}, {"opened", 9 * time.Minute}, {"delivered", 3 * time.Minute}}, 1, 0, ptr(base.Add(time.Minute))},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, id, named := sentCampaign(t)
			ctx := context.Background()
			key := keyOf(t, h, named["accepted"])
			for _, e := range tt.events {
				if err := h.service.RecordDeliveryEvent(ctx, key, e.name, base.Add(e.after)); err != nil {
					t.Fatalf("record %s: %v", e.name, err)
				}
			}
			got := lifecycleOf(t, h, id)
			if got.Accepted != 1 || got.Delivered != tt.delivered || got.Bounced != tt.bounced || got.Waiting != 0 {
				t.Fatalf("lifecycle: %+v", got)
			}
			rows, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.ID != named["accepted"] {
					continue
				}
				if row.Status != "SENT" {
					t.Fatalf("a report changed the result: %s", row.Status)
				}
				if (tt.deliveredAt == nil) != (row.DeliveredAt == nil) || (tt.deliveredAt != nil && !row.DeliveredAt.Equal(*tt.deliveredAt)) {
					t.Fatalf("delivered at %v, want %v", row.DeliveredAt, tt.deliveredAt)
				}
				if tt.bounced == 1 && row.BouncedAt == nil {
					t.Fatal("no bounce time")
				}
			}
		})
	}
}

func ptr[T any](value T) *T { return &value }
