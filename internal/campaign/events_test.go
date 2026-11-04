package campaign_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
