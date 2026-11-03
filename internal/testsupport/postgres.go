// Package testsupport provides helpers for integration tests that need real
// infrastructure. Missing infrastructure fails the test instead of skipping it,
// so a green run always means the reliability tests actually ran.
package testsupport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/negativexq/smart-campaign-composer/internal/db"
)

const defaultDatabaseURL = "postgres://scc:scc@localhost:55432/scc_test?sslmode=disable"

// NewDatabase creates an empty, fully migrated database that is dropped when
// the test ends, so tests can run in parallel without sharing state.
func NewDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return connect(t, NewDatabaseURL(t))
}

// NewDatabaseURL is like NewDatabase but returns the connection string, for
// tests that exercise connection handling themselves.
func NewDatabaseURL(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminURL := os.Getenv("TEST_DATABASE_URL")
	if adminURL == "" {
		adminURL = defaultDatabaseURL
	}
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to test postgres (run `make test-env-up`): %v", err)
	}
	defer admin.Close(ctx)

	name := "test_" + randomSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() { dropDatabase(t, adminURL, name) })

	parsed, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse test database url: %v", err)
	}
	parsed.Path = "/" + name
	databaseURL := parsed.String()

	if err := db.Migrate(ctx, databaseURL); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	return databaseURL
}

func connect(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func dropDatabase(t *testing.T, adminURL, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Errorf("connect to drop %s: %v", name, err)
		return
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Errorf("drop database %s: %v", name, err)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random suffix: %v", err)
	}
	return hex.EncodeToString(b)
}
