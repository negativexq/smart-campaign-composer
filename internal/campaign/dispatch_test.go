package campaign_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
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
