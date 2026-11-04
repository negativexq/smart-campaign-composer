// Command setup applies migrations and loads the seed. It runs once before the
// long-lived services start.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/asset"
	"github.com/negativexq/smart-campaign-composer/internal/db"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/server"
	"github.com/negativexq/smart-campaign-composer/internal/starter"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

func main() {
	slog.SetDefault(telemetry.NewLogger(os.Stdout))
	if err := run(); err != nil {
		slog.Error("setup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	databaseURL, err := server.RequireEnv("DATABASE_URL")
	if err != nil {
		return err
	}
	userCount, err := strconv.Atoi(server.Env("SEED_USER_COUNT", strconv.Itoa(seed.DefaultUserCount)))
	if err != nil {
		return fmt.Errorf("parse SEED_USER_COUNT: %w", err)
	}

	if err := db.Migrate(ctx, databaseURL); err != nil {
		return err
	}
	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	applied, err := seed.Apply(ctx, pool, seed.Generate(time.Now(), userCount))
	if err != nil {
		return err
	}
	started := false
	if dir := os.Getenv("ASSET_DIR"); dir != "" {
		if started, err = starter.Load(ctx, pool, asset.DiskStore{Dir: dir}); err != nil {
			return err
		}
	}
	slog.Info("setup complete", "seeded", applied, "starter_templates", started)
	return nil
}
