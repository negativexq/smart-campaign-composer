package delivery_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

// contactRig holds two campaigns with a seven-day "not messaged recently" rule sent to
// the same people, cut down to one message each for one person on one channel, so a
// test decides exactly which messages meet. mine belongs to the rig's campaign.
type contactRig struct {
	*rig
	other            string
	user             int64
	channel          string
	mine, theirs     string
	mineRun, sibling string
}

func newContactRig(t *testing.T) *contactRig {
	t.Helper()
	week := 7
	return newContactRigWith(t, &week, &week)
}

// newContactRigWith is newContactRig with the rule, or none, for each campaign.
func newContactRigWith(t *testing.T, mineRule, theirRule *int) *contactRig {
	t.Helper()
	ctx := context.Background()
	chosen := campaign.NewCampaign{Brief: brief, Channels: []string{"email", "sms"}, NotMessagedWithinDays: mineRule}
	r := newRigFor(t, chosen, always(accepted, nil))
	chosen.NotMessagedWithinDays = theirRule
	c := &contactRig{rig: r, other: dispatched(t, r.service, chosen)}
	if err := r.pool.QueryRow(ctx, `
		SELECT m.id::text, o.id::text, m.user_id, m.channel, m.run_id::text
		FROM campaign_deliveries m
		JOIN campaign_deliveries o ON o.user_id = m.user_id AND o.channel = m.channel
		WHERE m.campaign_id = $1 AND o.campaign_id = $2 AND m.status = 'QUEUED' AND o.status = 'QUEUED'
		  AND EXISTS (SELECT 1 FROM campaign_deliveries s WHERE s.campaign_id = $1 AND s.user_id = m.user_id AND s.channel <> m.channel)
		ORDER BY m.user_id, m.channel LIMIT 1`, r.id, c.other).Scan(&c.mine, &c.theirs, &c.user, &c.channel, &c.mineRun); err != nil {
		t.Fatalf("find a person both campaigns message: %v", err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT id::text FROM campaign_deliveries WHERE campaign_id = $1 AND user_id = $2 AND channel <> $3`,
		r.id, c.user, c.channel).Scan(&c.sibling); err != nil {
		t.Fatalf("find the other channel: %v", err)
	}
	// Everything else is out of the way.
	c.exec(t, `UPDATE campaign_deliveries SET status = 'SKIPPED' WHERE id::text <> ALL($1)`, []string{c.mine, c.theirs})
	return c
}

func (c *contactRig) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := c.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// queueMine puts the rule's message back in the queue, due now, with nothing tried.
func (c *contactRig) queueMine(t *testing.T) {
	t.Helper()
	c.exec(t, `
		UPDATE campaign_deliveries SET status = 'QUEUED', attempts = 0, next_attempt_at = now(), claim_token = NULL,
		       lease_expires_at = NULL, last_error = NULL, sent_at = NULL, updated_at = now()
		WHERE id = $1`, c.mine)
	c.exec(t, `DELETE FROM delivery_attempts WHERE delivery_id = $1`, c.mine)
}

// setTheirs puts the other campaign's message in a status it reached ago, not due
// again within the test.
func (c *contactRig) setTheirs(t *testing.T, status, ago string) {
	t.Helper()
	c.exec(t, `
		UPDATE campaign_deliveries
		SET status = $2, updated_at = now() - $3::interval, next_attempt_at = now() + interval '1 hour',
		    claim_token = CASE WHEN $2 = 'SENDING' THEN gen_random_uuid() END,
		    lease_expires_at = CASE WHEN $2 = 'SENDING' THEN now() + interval '1 minute' END
		WHERE id = $1`, c.theirs, status, ago)
	c.exec(t, `DELETE FROM delivery_attempts WHERE delivery_id = $1`, c.theirs)
}

// triedTheirs records a call for the other campaign's message, made ago.
func (c *contactRig) triedTheirs(t *testing.T, outcome, ago string) {
	t.Helper()
	c.exec(t, `
		INSERT INTO delivery_attempts (delivery_id, campaign_id, run_id, channel, attempt, outcome, occurred_at)
		SELECT id, campaign_id, run_id, channel, 1, $2, now() - $3::interval FROM campaign_deliveries WHERE id = $1`,
		c.theirs, outcome, ago)
}

type deliveryRow struct {
	status, reason string
	attempts       int
	claimed        bool
	tries          int
	waits          bool
}

func (c *contactRig) row(t *testing.T, id string) deliveryRow {
	t.Helper()
	var row deliveryRow
	if err := c.pool.QueryRow(context.Background(), `
		SELECT status, COALESCE(last_error, ''), attempts, claim_token IS NOT NULL OR lease_expires_at IS NOT NULL,
		       (SELECT count(*) FROM delivery_attempts a WHERE a.delivery_id = d.id), next_attempt_at > now()
		FROM campaign_deliveries d WHERE id = $1`, id).Scan(&row.status, &row.reason, &row.attempts, &row.claimed, &row.tries, &row.waits); err != nil {
		t.Fatal(err)
	}
	return row
}

// finishTheirs closes the other campaign's run at a time relative to when queuing
// counted recent messages for mine.
func (c *contactRig) finishTheirs(t *testing.T, sinceMine string) {
	t.Helper()
	c.exec(t, `
		UPDATE campaign_runs SET finished_at = (SELECT rule_as_of FROM campaign_runs WHERE id = $2) + $3::interval,
		       total = 0, counts = '[]', responses = '{}'
		WHERE campaign_id = $1`, c.other, c.mineRun, sinceMine)
}

// checks counts the checks the worker made before sending.
func (c *contactRig) checks() int {
	return testutil.CollectAndCount(c.metrics.DeliveryContactCheck)
}

func (c *contactRig) calls() int {
	c.sender.mu.Lock()
	defer c.sender.mu.Unlock()
	return len(c.sender.calls)
}

func (c *contactRig) process(t *testing.T) {
	t.Helper()
	if _, err := c.worker.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("process: %v", err)
	}
}

// The rule's days are read from the version as the worker reads them, and match
// what the campaign shows, for a new campaign and for a copy given another rule.
func TestTheWorkerReadsTheRuleTheCampaignShows(t *testing.T) {
	t.Parallel()
	c := newContactRig(t)
	ctx := context.Background()
	month := 30
	copied, err := c.service.Duplicate(ctx, c.id, &month)
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	for _, id := range []string{c.id, copied.ID} {
		view, err := c.service.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var read *int
		if err := c.pool.QueryRow(ctx, `
			SELECT (segment #>> '{segment,not_messaged_within_days}')::int FROM campaign_versions
			WHERE campaign_id = $1 AND version = $2`, id, view.Version).Scan(&read); err != nil {
			t.Fatal(err)
		}
		shown := view.Proposal.Segment.NotMessagedWithinDays
		if read == nil || shown == nil || *read != *shown {
			t.Fatalf("campaign %s: the worker reads %v, the campaign shows %v", id, read, shown)
		}
	}
}

// Under a seven-day rule, what another send left counts by when it happened: a sent
// or possibly sent message by its last change, and a retry after a call that got no
// answer by when that call was made. A failure or a refusal reached nobody.
func TestTheRuleCountsOtherSendsByWhenTheyHappened(t *testing.T) {
	t.Parallel()
	c := newContactRig(t)
	cases := []struct {
		name    string
		status  string
		ago     string
		tried   string // outcome of a call recorded for theirs, if any
		triedAt string
		want    string // what happens to mine
	}{
		{"sent within the rule", "SENT", "6 days", "", "", "SKIPPED"},
		{"sent before the rule", "SENT", "8 days", "", "", "SENT"},
		{"possibly sent within the rule", "UNKNOWN", "6 days", "", "", "SKIPPED"},
		{"possibly sent before the rule", "UNKNOWN", "8 days", "", "", "SENT"},
		{"retrying after a call that got no answer", "RETRYING", "0 days", "timeout", "6 days", "SKIPPED"},
		// The retry was changed today, but the call that may have got through is old.
		{"retrying after an old call that got no answer", "RETRYING", "0 days", "timeout", "10 days", "SENT"},
		{"retrying after a server error", "RETRYING", "0 days", "server_error", "0 days", "SENT"},
		{"failed", "FAILED", "0 days", "rejected", "0 days", "SENT"},
		{"skipped", "SKIPPED", "0 days", "", "", "SENT"},
	}
	for _, tc := range cases {
		c.queueMine(t)
		c.setTheirs(t, tc.status, tc.ago)
		if tc.tried != "" {
			c.triedTheirs(t, tc.tried, tc.triedAt)
		}
		before := c.calls()

		c.process(t)

		row := c.row(t, c.mine)
		if row.status != tc.want {
			t.Errorf("%s: mine ended %s (%s), want %s", tc.name, row.status, row.reason, tc.want)
			continue
		}
		sent := c.calls() - before
		switch tc.want {
		case "SENT":
			if sent != 1 {
				t.Errorf("%s: %d calls, want 1", tc.name, sent)
			}
		case "SKIPPED":
			if sent != 0 || row.reason != "messaged_recently" || row.attempts != 0 || row.tries != 0 || row.claimed {
				t.Errorf("%s: %d calls, row %+v", tc.name, sent, row)
			}
		}
	}
}

// A message on its way to the person holds the rule's message back without using
// one of its attempts, and once that one has gone the rule's message is skipped.
func TestTheRuleWaitsForAMessageBeingSent(t *testing.T) {
	t.Parallel()
	c := newContactRig(t)
	c.setTheirs(t, "SENDING", "0 days")

	c.process(t)

	row := c.row(t, c.mine)
	if row.status != "RETRYING" || row.reason != "another_message_sending" || row.attempts != 0 || row.tries != 0 || row.claimed || !row.waits {
		t.Fatalf("held back: %+v", row)
	}
	if c.calls() != 0 {
		t.Fatalf("calls: %d", c.calls())
	}

	c.setTheirs(t, "SENT", "0 days")
	c.exec(t, `UPDATE campaign_deliveries SET next_attempt_at = now() WHERE id = $1`, c.mine)
	c.process(t)
	if row := c.row(t, c.mine); row.status != "SKIPPED" || row.reason != "messaged_recently" {
		t.Fatalf("after the other one went: %+v", row)
	}
	if c.calls() != 0 {
		t.Fatalf("calls: %d", c.calls())
	}
}

// The other channel of the same send is part of it, not a message that came before.
func TestTheRuleLeavesTheSameSendAlone(t *testing.T) {
	t.Parallel()
	c := newContactRig(t)
	c.setTheirs(t, "SKIPPED", "0 days")
	c.exec(t, `UPDATE campaign_deliveries SET status = 'SENT', updated_at = now() WHERE id = $1`, c.sibling)

	c.process(t)

	if row := c.row(t, c.mine); row.status != "SENT" {
		t.Fatalf("mine: %+v", row)
	}
	if c.calls() != 1 {
		t.Fatalf("calls: %d", c.calls())
	}
}

// A message whose claim moved on while it was being checked is neither sent nor
// changed: whoever holds it now decides.
func TestTheRuleSendsNothingForALostClaim(t *testing.T) {
	t.Parallel()
	c := newContactRig(t)
	c.setTheirs(t, "SENDING", "0 days")
	c.limiter.onSettle = func(channel string, used int) {
		if channel == c.channel && used == 1 {
			c.exec(t, `UPDATE campaign_deliveries SET claim_token = gen_random_uuid() WHERE id = $1`, c.mine)
		}
	}

	c.process(t)

	if row := c.row(t, c.mine); row.status != "SENDING" || row.reason != "" || row.attempts != 0 {
		t.Fatalf("mine: %+v", row)
	}
	if c.calls() != 0 {
		t.Fatalf("calls: %d", c.calls())
	}
}

type lockWait struct {
	pid       int32
	kind      string
	blockedBy []int32
}

// lockWaits lists the sessions in this test's database waiting for a lock.
func (c *contactRig) lockWaits(t *testing.T) []lockWait {
	t.Helper()
	rows, err := c.pool.Query(context.Background(), `
		SELECT l.pid, l.locktype, pg_blocking_pids(l.pid)
		FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
		WHERE NOT l.granted AND a.datname = current_database()
		ORDER BY l.pid`)
	if err != nil {
		t.Fatal(err)
	}
	waits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (lockWait, error) {
		var w lockWait
		err := row.Scan(&w.pid, &w.kind, &w.blockedBy)
		return w, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return waits
}

// Two messages to one person, both with the rule and claimed in the same round, check
// at the same moment. The checks take turns: the first sees the second being sent and
// steps back, writing so before the second looks, so the second goes. Without the
// turns both would see the other being sent and both would step back.
//
// The test holds both rows after the claim, so the first check stops at its write
// while holding the person's lock, and the second stops at that lock. Letting go then
// shows the order the lock gives.
func TestTwoRulesClaimedTogetherSendOnce(t *testing.T) {
	t.Parallel()
	claimedTogether(t, newContactRig(t))
}

// A message without the rule and one with it, claimed in the same round, take the
// same turns: whichever looks first waits, so the one with the rule never goes after
// the other.
func TestARuleAndNoRuleClaimedTogetherSendOnce(t *testing.T) {
	t.Parallel()
	week := 7
	claimedTogether(t, newContactRigWith(t, nil, &week))
}

func claimedTogether(t *testing.T, c *contactRig) {
	t.Helper()
	ctx := context.Background()
	c.exec(t, `UPDATE campaign_deliveries SET status = 'QUEUED', next_attempt_at = now() WHERE id = $1`, c.theirs)

	claimed, release := make(chan struct{}), make(chan struct{})
	c.limiter.onSettle = func(channel string, used int) {
		if channel == c.channel && used == 2 {
			close(claimed)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.worker.ProcessBatch(ctx)
		done <- err
	}()
	select {
	case <-claimed:
	case <-time.After(10 * time.Second):
		t.Fatal("the two messages were not claimed together")
	}

	hold, err := c.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(ctx) //nolint:errcheck // released below
	var holder int32
	if err := hold.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `SELECT 1 FROM campaign_deliveries WHERE id::text = ANY($1) FOR UPDATE`, []string{c.mine, c.theirs}); err != nil {
		t.Fatal(err)
	}
	close(release)

	// The first check waits on the row this test holds, the second on the person's
	// lock the first one holds.
	var waits []lockWait
	deadline := time.Now().Add(10 * time.Second)
	for {
		waits = c.lockWaits(t)
		if len(waits) == 2 {
			byKind := map[string]lockWait{}
			for _, w := range waits {
				byKind[w.kind] = w
			}
			row, onRow := byKind["transactionid"]
			turn, onTurn := byKind["advisory"]
			if onRow && onTurn && slices.Equal(row.blockedBy, []int32{holder}) && slices.Equal(turn.blockedBy, []int32{row.pid}) &&
				row.pid != holder && turn.pid != holder {
				break
			}
		}
		if time.Now().After(deadline) {
			_ = hold.Rollback(ctx)
			t.Fatalf("the checks did not wait as expected (test session %d): %+v", holder, waits)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("process: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the round did not finish")
	}

	mine, theirs := c.row(t, c.mine), c.row(t, c.theirs)
	statuses := []string{mine.status, theirs.status}
	slices.Sort(statuses)
	if fmt.Sprint(statuses) != "[RETRYING SENT]" || c.calls() != 1 {
		t.Fatalf("mine %+v, theirs %+v, calls %d", mine, theirs, c.calls())
	}
	held := mine
	if theirs.status == "RETRYING" {
		held = theirs
	}
	if held.reason != "another_message_sending" || held.attempts != 0 || held.tries != 0 || held.claimed {
		t.Fatalf("held back: %+v", held)
	}
}

// A campaign sending while no other run has been open since its queuing counted
// checks nothing, with the rule or without it, even with another message to the
// person on its way: queuing already left out what went before.
func TestACampaignSendingAloneChecksNothing(t *testing.T) {
	t.Parallel()
	week := 7
	for _, mineRule := range []*int{&week, nil} {
		c := newContactRigWith(t, mineRule, &week)
		c.setTheirs(t, "SENDING", "0 days")
		c.finishTheirs(t, "-2 minutes")

		c.process(t)

		if row := c.row(t, c.mine); row.status != "SENT" || c.calls() != 1 || c.checks() != 0 {
			t.Fatalf("rule %v: mine %+v, calls %d, checks %d", mineRule, row, c.calls(), c.checks())
		}
	}
}

// Two campaigns without the rule check nothing however they overlap.
func TestTwoCampaignsWithoutTheRuleCheckNothing(t *testing.T) {
	t.Parallel()
	c := newContactRigWith(t, nil, nil)
	c.setTheirs(t, "SENDING", "0 days")

	c.process(t)

	if row := c.row(t, c.mine); row.status != "SENT" || c.calls() != 1 || c.checks() != 0 {
		t.Fatalf("mine %+v, calls %d, checks %d", row, c.calls(), c.checks())
	}
}

// A run that has finished since queuing counted for mine may have reached the person
// in between, so it still counts, and so does one that finished just before, within
// the margin for clocks.
func TestARunFinishedSinceMineWasQueuedStillCounts(t *testing.T) {
	t.Parallel()
	for _, since := range []string{"1 second", "-30 seconds"} {
		c := newContactRig(t)
		c.setTheirs(t, "SENT", "0 days")
		c.finishTheirs(t, since)

		c.process(t)

		if row := c.row(t, c.mine); row.status != "SKIPPED" || row.reason != "messaged_recently" || c.calls() != 0 {
			t.Fatalf("finished %s after: mine %+v, calls %d", since, row, c.calls())
		}
	}
}

// A message without the rule waits, without using an attempt, while a message with
// the rule to the same person is being sent, and goes once that one has.
func TestAMessageWithoutTheRuleWaitsForOneWithIt(t *testing.T) {
	t.Parallel()
	week := 7
	c := newContactRigWith(t, nil, &week)
	c.setTheirs(t, "SENDING", "0 days")

	c.process(t)

	row := c.row(t, c.mine)
	if row.status != "RETRYING" || row.reason != "another_message_sending" || row.attempts != 0 || row.tries != 0 || row.claimed || !row.waits {
		t.Fatalf("held back: %+v", row)
	}
	if c.calls() != 0 {
		t.Fatalf("calls: %d", c.calls())
	}

	c.setTheirs(t, "SENT", "0 days")
	c.exec(t, `UPDATE campaign_deliveries SET next_attempt_at = now() WHERE id = $1`, c.mine)
	c.process(t)
	if row := c.row(t, c.mine); row.status != "SENT" || c.calls() != 1 {
		t.Fatalf("after the other one went: %+v, calls %d", row, c.calls())
	}
}

// A message whose own earlier call got no answer may already be with the provider, so
// the rule never skips it: it is retried with the same key and ends as the provider
// says. One whose earlier call failed reached nobody and is still skipped.
func TestTheRuleNeverSkipsAMessageThatMayHaveArrived(t *testing.T) {
	t.Parallel()
	c := newContactRig(t)
	cases := []struct {
		tried string
		want  string
	}{
		{"timeout", "SENT"},
		{"server_error", "SKIPPED"},
	}
	for _, tc := range cases {
		c.queueMine(t)
		c.exec(t, `UPDATE campaign_deliveries SET status = 'RETRYING', attempts = 1 WHERE id = $1`, c.mine)
		c.exec(t, `
			INSERT INTO delivery_attempts (delivery_id, campaign_id, run_id, channel, attempt, outcome, occurred_at)
			SELECT id, campaign_id, run_id, channel, 1, $2, now() - interval '1 minute' FROM campaign_deliveries WHERE id = $1`,
			c.mine, tc.tried)
		c.setTheirs(t, "SENT", "0 days")
		var key string
		if err := c.pool.QueryRow(context.Background(), `SELECT idempotency_key FROM campaign_deliveries WHERE id = $1`, c.mine).Scan(&key); err != nil {
			t.Fatal(err)
		}
		before := c.calls()

		c.process(t)

		row := c.row(t, c.mine)
		if row.status != tc.want {
			t.Fatalf("after %s: mine %+v", tc.tried, row)
		}
		switch tc.want {
		case "SENT":
			c.sender.mu.Lock()
			last := c.sender.calls[len(c.sender.calls)-1]
			c.sender.mu.Unlock()
			if c.calls()-before != 1 || last.IdempotencyKey != key {
				t.Fatalf("after %s: %d calls, key %q, want %q", tc.tried, c.calls()-before, last.IdempotencyKey, key)
			}
		case "SKIPPED":
			if c.calls() != before || row.reason != "messaged_recently" {
				t.Fatalf("after %s: %d calls, mine %+v", tc.tried, c.calls()-before, row)
			}
		}
	}
}
