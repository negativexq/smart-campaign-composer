package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/negativexq/smart-campaign-composer/internal/db"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

const (
	pgUniqueViolation = "23505"
	pgCheckViolation  = "23514"
	pgRaiseException  = "P0001"
)

func requirePgError(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("want postgres error %s, got: %v", code, err)
	}
	if pgErr.Code != code {
		t.Fatalf("want postgres error %s, got %s: %s", code, pgErr.Code, pgErr.Message)
	}
}

func insertUser(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, first_name, city, email, phone, language)
		VALUES ($1, 'Test', 'Ankara', $2, $3, 'tr')`,
		id, fmt.Sprintf("u%d@example.test", id), fmt.Sprintf("+9050%d", id))
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func insertCampaign(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO campaigns (brief, name) VALUES ('brief', 'name') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("insert campaign: %v", err)
	}
	return id
}

func insertVersion(t *testing.T, pool *pgxpool.Pool, campaignID string, version int) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO campaign_versions (campaign_id, version, segment, content)
		VALUES ($1, $2, '{}', '{}') RETURNING id`, campaignID, version).Scan(&id)
	if err != nil {
		t.Fatalf("insert version: %v", err)
	}
	return id
}

func insertDelivery(pool *pgxpool.Pool, campaignID, versionID string, userID int64, channel, key string) error {
	return insertDeliveryInRun(pool, campaignID, versionID, 1, userID, channel, key)
}

// insertDeliveryInRun adds a delivery to the campaign's run with that number,
// opening the run if it is not there yet.
func insertDeliveryInRun(pool *pgxpool.Pool, campaignID, versionID string, run int, userID int64, channel, key string) error {
	_, err := pool.Exec(context.Background(), `
		WITH opened AS (
			INSERT INTO campaign_runs (campaign_id, number) VALUES ($1, $6)
			ON CONFLICT (campaign_id, number) DO UPDATE SET number = EXCLUDED.number
			RETURNING id
		)
		INSERT INTO campaign_deliveries (campaign_id, run_id, campaign_version_id, user_id, channel, idempotency_key)
		SELECT $1, opened.id, $2, $3, $4, $5 FROM opened`, campaignID, versionID, userID, channel, key, run)
	return err
}

func TestMigrateCreatesAllTables(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)

	for _, table := range []string{
		"users", "consents", "cart_events", "purchase_events",
		"campaigns", "campaign_versions", "campaign_runs", "campaign_deliveries", "delivery_attempts",
	} {
		var exists bool
		err := pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s is missing", table)
		}
	}
}

func TestMigrateCanRunTwice(t *testing.T) {
	t.Parallel()
	databaseURL := testsupport.NewDatabaseURL(t)

	if err := db.Migrate(context.Background(), databaseURL); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
}

func TestConsentRejectsUnknownChannel(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	insertUser(t, pool, 1)

	_, err := pool.Exec(context.Background(),
		`INSERT INTO consents (user_id, channel, allowed) VALUES (1, 'push', true)`)

	requirePgError(t, err, pgCheckViolation)
}

func TestCampaignRejectsUnknownStatus(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)

	_, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns (brief, name, status) VALUES ('brief', 'name', 'SENDING_MAYBE')`)

	requirePgError(t, err, pgCheckViolation)
}

func TestDeliveryAllowsOnePerRecipientAndChannel(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	insertUser(t, pool, 1)
	campaign := insertCampaign(t, pool)
	v1 := insertVersion(t, pool, campaign, 1)

	if err := insertDelivery(pool, campaign, v1, 1, "sms", "key-sms"); err != nil {
		t.Fatalf("first sms delivery: %v", err)
	}
	if err := insertDelivery(pool, campaign, v1, 1, "email", "key-email"); err != nil {
		t.Fatalf("same recipient on another channel must be allowed: %v", err)
	}

	err := insertDelivery(pool, campaign, v1, 1, "sms", "key-sms-again")
	requirePgError(t, err, pgUniqueViolation)
}

// A run approved again as a new version must not message people the earlier
// version already reached in it.
func TestDeliveryUniquenessSpansCampaignVersions(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	insertUser(t, pool, 1)
	campaign := insertCampaign(t, pool)
	v1 := insertVersion(t, pool, campaign, 1)
	v2 := insertVersion(t, pool, campaign, 2)

	if err := insertDelivery(pool, campaign, v1, 1, "sms", "key-v1"); err != nil {
		t.Fatalf("delivery for v1: %v", err)
	}

	err := insertDelivery(pool, campaign, v2, 1, "sms", "key-v2")
	requirePgError(t, err, pgUniqueViolation)
}

// A repeating campaign may reach someone again in a later run.
func TestALaterRunCanReachTheSamePerson(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	insertUser(t, pool, 1)
	campaign := insertCampaign(t, pool)
	v1 := insertVersion(t, pool, campaign, 1)

	if err := insertDeliveryInRun(pool, campaign, v1, 1, 1, "sms", "run-1"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := insertDeliveryInRun(pool, campaign, v1, 2, 1, "sms", "run-2"); err != nil {
		t.Fatalf("a later run must be allowed to reach the same person: %v", err)
	}
}

func TestDeliveryIdempotencyKeyIsUnique(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	insertUser(t, pool, 1)
	insertUser(t, pool, 2)
	campaign := insertCampaign(t, pool)
	v1 := insertVersion(t, pool, campaign, 1)

	if err := insertDelivery(pool, campaign, v1, 1, "sms", "same-key"); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	err := insertDelivery(pool, campaign, v1, 2, "sms", "same-key")
	requirePgError(t, err, pgUniqueViolation)
}

func TestDeliveryStatusValues(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	insertUser(t, pool, 1)
	campaign := insertCampaign(t, pool)
	v1 := insertVersion(t, pool, campaign, 1)
	if err := insertDelivery(pool, campaign, v1, 1, "sms", "key"); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	ctx := context.Background()

	// UNKNOWN records a send whose outcome could not be confirmed. It must stay
	// distinct from FAILED because the provider may have delivered the message.
	for _, status := range []string{"QUEUED", "SENDING", "SENT", "RETRYING", "FAILED", "SKIPPED", "UNKNOWN"} {
		if _, err := pool.Exec(ctx, `UPDATE campaign_deliveries SET status = $1`, status); err != nil {
			t.Errorf("status %s must be allowed: %v", status, err)
		}
	}

	_, err := pool.Exec(ctx, `UPDATE campaign_deliveries SET status = 'LOST'`)
	requirePgError(t, err, pgCheckViolation)
}

func TestVersionIsEditableUntilApproved(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	campaign := insertCampaign(t, pool)
	version := insertVersion(t, pool, campaign, 1)
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`UPDATE campaign_versions SET content = '{"sms":"edited"}' WHERE id = $1`, version); err != nil {
		t.Fatalf("draft version must be editable: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE campaign_versions SET approved_at = now() WHERE id = $1`, version); err != nil {
		t.Fatalf("approval must be allowed: %v", err)
	}
}

func TestApprovedVersionIsImmutable(t *testing.T) {
	t.Parallel()
	pool := testsupport.NewDatabase(t)
	campaign := insertCampaign(t, pool)
	version := insertVersion(t, pool, campaign, 1)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`UPDATE campaign_versions SET approved_at = now() WHERE id = $1`, version); err != nil {
		t.Fatalf("approve: %v", err)
	}

	for name, statement := range map[string]string{
		"content":     `UPDATE campaign_versions SET content = '{"sms":"tampered"}' WHERE id = $1`,
		"segment":     `UPDATE campaign_versions SET segment = '{"city":"Ankara"}' WHERE id = $1`,
		"approved_at": `UPDATE campaign_versions SET approved_at = NULL WHERE id = $1`,
		"delete":      `DELETE FROM campaign_versions WHERE id = $1`,
	} {
		_, err := pool.Exec(ctx, statement, version)
		t.Run(name, func(t *testing.T) { requirePgError(t, err, pgRaiseException) })
	}
}

func TestAppConnectionsDoNotCompileQueries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databaseURL := testsupport.NewDatabaseURL(t)
	for url, want := range map[string]string{databaseURL: "off", databaseURL + "&jit=on": "on"} {
		pool, err := db.Connect(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		var jit string
		err = pool.QueryRow(ctx, "SHOW jit").Scan(&jit)
		pool.Close()
		if err != nil || jit != want {
			t.Fatalf("%s: jit %q %v, want %q", url, jit, err, want)
		}
	}
}
