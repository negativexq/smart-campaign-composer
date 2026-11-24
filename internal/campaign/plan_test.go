package campaign_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

// planJoins lists the join nodes of a plan and adds up the rows they read and threw
// away, which is where a join comparing every row with every other one shows.
func planJoins(t *testing.T, plan []byte) ([]string, float64) {
	t.Helper()
	var root []struct {
		Plan map[string]any `json:"Plan"`
	}
	if err := json.Unmarshal(plan, &root); err != nil || len(root) == 0 {
		t.Fatalf("read plan %s: %v", plan, err)
	}
	var joins []string
	var removed float64
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		switch kind, _ := node["Node Type"].(string); kind {
		case "Nested Loop", "Hash Join", "Merge Join":
			joins = append(joins, kind)
		}
		thrown, _ := node["Rows Removed by Join Filter"].(float64)
		loops, _ := node["Actual Loops"].(float64)
		removed += thrown * max(loops, 1)
		children, _ := node["Plans"].([]any)
		for _, child := range children {
			if child, ok := child.(map[string]any); ok {
				walk(child)
			}
		}
	}
	walk(root[0].Plan)
	return joins, removed
}

// A campaign's statistics are often older than the campaign itself, and then
// PostgreSQL may plan as if it had a few rows. The report once joined the campaign's
// grouped events to its deliveries, and on such a guess picked a nested loop that
// compared every delivery with every event. The report's counts join nothing now, so
// no guess can pick that loop; this keeps it so, with hash and merge joins and
// sequential scans off, the way a large table with old statistics is planned.
func TestReportCountsJoinNothingSoNoPlanCanCompareEveryRow(t *testing.T) {
	t.Parallel()
	h, id, _ := sentCampaign(t)
	ctx := context.Background()
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO delivery_events (delivery_id, campaign_id, event_type, occurred_at)
		SELECT id, campaign_id, 'delivered', now() FROM campaign_deliveries WHERE campaign_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `UPDATE campaign_deliveries SET variant = CASE WHEN user_id % 2 = 0 THEN 'A' ELSE 'B' END WHERE campaign_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	var deliveries float64
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM campaign_deliveries WHERE campaign_id = $1`, id).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries < 10 {
		t.Fatalf("too few deliveries for a plan worth reading: %v", deliveries)
	}

	for name, query := range map[string]string{"lifecycle": campaign.LifecycleQuery, "variants": campaign.VariantsQuery} {
		var plan []byte
		err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL enable_hashjoin = off; SET LOCAL enable_mergejoin = off; SET LOCAL enable_seqscan = off`); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `EXPLAIN (ANALYZE, FORMAT JSON) `+query, id, nil).Scan(&plan)
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if joins, removed := planJoins(t, plan); len(joins) > 0 || removed > 0 {
			t.Errorf("%s: the plan joins %v and threw away %v rows for %v deliveries: %s", name, joins, removed, deliveries, plan)
		}
	}

	report, err := h.service.Report(ctx, id)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	arrived := 0
	for _, channel := range report.Lifecycle {
		arrived += channel.Delivered
	}
	if arrived != 1 || len(report.Variants) == 0 {
		t.Fatalf("one sent message has a delivered report, and both versions are counted: %+v %+v", report.Lifecycle, report.Variants)
	}
}

// The monitor's counts read only the rows it shows. A reused plan cannot see which
// statuses $1 holds, so the query names them itself; this checks that such a plan
// still reads the partial index, and that the names match the monitor's list.
func TestMonitorCountsReadOnlyMonitoredRowsInAnyPlan(t *testing.T) {
	t.Parallel()
	h, _, _ := sentCampaign(t)
	ctx := context.Background()
	for _, status := range campaign.MonitorStatuses {
		if !strings.Contains(campaign.MonitorMatching, "'"+status+"'") {
			t.Errorf("the monitor's query does not name %s", status)
		}
	}
	var plan []byte
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	err = func() error {
		if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off; SET LOCAL plan_cache_mode = force_generic_plan`); err != nil {
			return err
		}
		// On a few rows any index is as good, so leave only this one to choose; the
		// transaction rolls the drops back.
		if _, err := tx.Exec(ctx, `
			DO $$
			DECLARE name text;
			BEGIN
				FOR name IN SELECT indexname FROM pg_indexes WHERE tablename = 'campaign_deliveries'
				            AND indexname <> 'campaign_deliveries_monitor_idx'
				            AND indexdef NOT LIKE 'CREATE UNIQUE%' LOOP
					EXECUTE format('DROP INDEX %I', name);
				END LOOP;
			END $$`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `PREPARE monitor_counts (text[], text, text, timestamptz, text) AS SELECT d.status, count(*)`+campaign.MonitorMatching+` GROUP BY d.status`); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `EXPLAIN (FORMAT JSON) EXECUTE monitor_counts('{QUEUED,FAILED}', '', '', NULL, '')`).Scan(&plan)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), "campaign_deliveries_monitor_idx") {
		t.Fatalf("a reused plan does not read the monitor's index: %s", plan)
	}
}
