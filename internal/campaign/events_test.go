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
