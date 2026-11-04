package campaign_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

const reportStart = "2026-11-03T10:00:04Z"

// sentCampaign leaves three queued deliveries with a known provider history:
// one accepted after a 429, one failed after two errors, one with no response.
func sentCampaign(t *testing.T) (*harness, string, map[string]string) {
	t.Helper()
	h, id := readyCampaign(t)
	ctx := context.Background()
	if _, err := h.service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := h.service.Dispatch(ctx, id); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	queued, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Status: "QUEUED"})
	if err != nil || len(queued) < 3 {
		t.Fatalf("queued: %d %v", len(queued), err)
	}
	named := map[string]string{"accepted": queued[0].ID, "failed": queued[1].ID, "unknown": queued[2].ID}
	finish := func(key, status, reason string) {
		t.Helper()
		if _, err := h.pool.Exec(ctx, `UPDATE campaign_deliveries SET status = $2, last_error = NULLIF($3, '') WHERE id = $1`, named[key], status, reason); err != nil {
			t.Fatal(err)
		}
	}
	finish("accepted", "SENT", "")
	finish("failed", "FAILED", "server_error")
	finish("unknown", "UNKNOWN", "timeout")

	attempt := func(key string, number int, outcome string, offset time.Duration) {
		t.Helper()
		start, err := time.Parse(time.RFC3339, reportStart)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO delivery_attempts (delivery_id, campaign_id, attempt, outcome, occurred_at)
			VALUES ($1, $2, $3, $4, $5)`, named[key], id, number, outcome, start.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	attempt("accepted", 1, "rate_limited", 0)
	attempt("accepted", 2, "accepted", 20*time.Second)
	attempt("failed", 1, "server_error", 20*time.Second)
	attempt("failed", 2, "server_error", 40*time.Second)
	attempt("unknown", 1, "timeout", 40*time.Second)
	return h, id, named
}

func TestReportExplainsWhatHappened(t *testing.T) {
	t.Parallel()
	h, id, _ := sentCampaign(t)
	report, err := h.service.Report(context.Background(), id)
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	reasons := map[string]int{}
	for _, reason := range report.Reasons {
		reasons[reason.Reason] = reason.Deliveries
	}
	if reasons["server_error"] != 1 || reasons["timeout"] != 1 || reasons["no_consent"] == 0 {
		t.Fatalf("reasons: %v", reasons)
	}

	problems := map[string]campaign.ReportProblem{}
	for _, problem := range report.Problems {
		problems[problem.Outcome] = problem
	}
	if _, listed := problems["accepted"]; listed {
		t.Fatal("an accepted call is not a problem")
	}
	if got := problems["server_error"]; got.Deliveries != 1 || got.Attempts != 2 {
		t.Fatalf("server errors: %+v", got)
	}
	if got := problems["rate_limited"]; got.Deliveries != 1 || got.Attempts != 1 {
		t.Fatalf("rate limited: %+v", got)
	}
}

func TestReportTimelineHasEveryBucketFromFirstCallToLast(t *testing.T) {
	t.Parallel()
	h, id, _ := sentCampaign(t)
	report, err := h.service.Report(context.Background(), id)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	timeline := report.Timeline
	if timeline.BucketSeconds != 1 || len(timeline.Buckets) != 41 {
		t.Fatalf("width %d, buckets %d", timeline.BucketSeconds, len(timeline.Buckets))
	}
	first, last := timeline.Buckets[0], timeline.Buckets[40]
	if first.At.UTC().Format(time.RFC3339) != reportStart || first.RateLimited != 1 {
		t.Fatalf("first: %+v", first)
	}
	if last.ServerError != 1 || last.Timeout != 1 {
		t.Fatalf("last: %+v", last)
	}
	if empty := timeline.Buckets[1]; empty.Accepted+empty.RateLimited+empty.ServerError+empty.Rejected+empty.Timeout != 0 {
		t.Fatalf("a quiet second must stay in the chart as zero: %+v", empty)
	}
	if middle := timeline.Buckets[20]; middle.Accepted != 1 || middle.ServerError != 1 {
		t.Fatalf("middle: %+v", middle)
	}
}

func TestReportTimelineWidensBucketsForALongSend(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	// A repeating campaign can run for weeks; the chart must stay readable.
	if _, err := h.pool.Exec(context.Background(), `
		INSERT INTO delivery_attempts (delivery_id, campaign_id, attempt, outcome, occurred_at)
		VALUES ($1, $2, 9, 'accepted', $3::timestamptz + interval '30 days')`, named["accepted"], id, reportStart); err != nil {
		t.Fatal(err)
	}
	report, err := h.service.Report(context.Background(), id)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if got := len(report.Timeline.Buckets); got == 0 || got > 61 {
		t.Fatalf("buckets: %d", got)
	}
	total := 0
	for _, bucket := range report.Timeline.Buckets {
		total += bucket.Accepted + bucket.RateLimited + bucket.ServerError + bucket.Timeout
	}
	if total != 6 {
		t.Fatalf("calls in the chart: %d", total)
	}
}

func TestReportOfACampaignNotSentIsEmpty(t *testing.T) {
	t.Parallel()
	h, id := readyCampaign(t)
	report, err := h.service.Report(context.Background(), id)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Reasons == nil || report.Problems == nil || report.Timeline.Buckets == nil || len(report.Timeline.Buckets) != 0 {
		t.Fatalf("report: %+v", report)
	}
	if _, err := h.service.Report(context.Background(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, campaign.ErrNotFound) {
		t.Fatalf("unknown campaign: %v", err)
	}
}

func TestDeliveriesFilterByWhatHappened(t *testing.T) {
	t.Parallel()
	h, id, named := sentCampaign(t)
	start, err := time.Parse(time.RFC3339, reportStart)
	if err != nil {
		t.Fatal(err)
	}
	at := func(offset time.Duration) *time.Time {
		value := start.Add(offset)
		return &value
	}
	tests := []struct {
		name   string
		filter campaign.DeliveryFilter
		want   []string
	}{
		{"had a 429 even though it was accepted later", campaign.DeliveryFilter{Outcome: "rate_limited"}, []string{"accepted"}},
		{"ended for this reason", campaign.DeliveryFilter{Reason: "timeout"}, []string{"unknown"}},
		{"called more than once", campaign.DeliveryFilter{Calls: "2+"}, []string{"accepted", "failed"}},
		{"called in this window", campaign.DeliveryFilter{From: at(20 * time.Second), To: at(21 * time.Second)}, []string{"accepted", "failed"}},
		{"window end is exclusive", campaign.DeliveryFilter{From: at(0), To: at(20 * time.Second)}, []string{"accepted"}},
		{"this outcome in this window", campaign.DeliveryFilter{Outcome: "server_error", From: at(40 * time.Second)}, []string{"failed"}},
		{"nothing matches both", campaign.DeliveryFilter{Outcome: "rate_limited", From: at(40 * time.Second)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := h.service.Deliveries(context.Background(), id, tt.filter)
			if err != nil {
				t.Fatalf("deliveries: %v", err)
			}
			got := map[string]bool{}
			for _, row := range rows {
				got[row.ID] = true
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d rows, want %v", len(got), tt.want)
			}
			for _, key := range tt.want {
				if !got[named[key]] {
					t.Fatalf("missing %s", key)
				}
			}
		})
	}
	if _, err := h.service.Deliveries(context.Background(), id, campaign.DeliveryFilter{Outcome: "nope"}); !errors.Is(err, campaign.ErrUnknownFilter) {
		t.Fatalf("bad outcome: %v", err)
	}
}

func TestDeliveriesAreFoundByCallsAndCity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, id, named := sentCampaign(t)
	count := func(filter campaign.DeliveryFilter) map[string]bool {
		t.Helper()
		rows, err := h.service.Deliveries(ctx, id, filter)
		if err != nil {
			t.Fatalf("deliveries %+v: %v", filter, err)
		}
		found := map[string]bool{}
		for _, row := range rows {
			found[row.ID] = true
		}
		return found
	}
	// The accepted one got a 429 first: two calls, though only one used up an attempt.
	rows, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Calls: "2", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Calls != 2 {
			t.Fatalf("the list says %d calls under a filter for two", row.Calls)
		}
	}
	once := count(campaign.DeliveryFilter{Calls: "1", Limit: 500})
	if len(once) != 1 || !once[named["unknown"]] {
		t.Fatalf("called once: %v", once)
	}
	twice := count(campaign.DeliveryFilter{Calls: "2", Limit: 500})
	if len(twice) != 2 || !twice[named["accepted"]] || !twice[named["failed"]] {
		t.Fatalf("called twice: %v", twice)
	}
	if more := count(campaign.DeliveryFilter{Calls: "2+", Limit: 500}); len(more) != 2 {
		t.Fatalf("two or more: %v", more)
	}
	if none := count(campaign.DeliveryFilter{Calls: "3+", Limit: 500}); len(none) != 0 {
		t.Fatalf("three or more: %v", none)
	}
	if never := count(campaign.DeliveryFilter{Calls: "0", Limit: 500}); never[named["accepted"]] {
		t.Fatalf("never called: %v", never)
	}
	if _, err := h.service.Deliveries(ctx, id, campaign.DeliveryFilter{Calls: "some"}); !errors.Is(err, campaign.ErrUnknownFilter) {
		t.Fatalf("bad calls: %v", err)
	}

	cities, err := h.service.DeliveryCities(ctx, id)
	if err != nil || len(cities) == 0 {
		t.Fatalf("cities: %v %v", cities, err)
	}
	if kept := count(campaign.DeliveryFilter{City: cities[0], Limit: 500}); len(kept) == 0 {
		t.Fatalf("city %s kept nobody", cities[0])
	}
	if kept := count(campaign.DeliveryFilter{City: "Nowhere", Limit: 500}); len(kept) != 0 {
		t.Fatalf("unknown city kept: %v", kept)
	}
}
