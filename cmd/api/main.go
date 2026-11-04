// Command api serves the campaign REST and SSE endpoints.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/asset"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/db"
	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/health"
	"github.com/negativexq/smart-campaign-composer/internal/httpapi"
	"github.com/negativexq/smart-campaign-composer/internal/server"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

func main() {
	logger := telemetry.NewLogger(os.Stdout)
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		slog.Error("api failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databaseURL, err := server.RequireEnv("DATABASE_URL")
	if err != nil {
		return err
	}
	provider, err := ai.NewFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	shutdownTracing, err := telemetry.SetupTracing(ctx, "api", os.Getenv)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			slog.Warn("flush traces", "error", err)
		}
	}()

	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	metrics := telemetry.NewMetrics(prometheus.DefaultRegisterer)
	campaigns := campaign.NewService(pool, provider, metrics, otel.Tracer("campaign"), time.Now)

	mux := http.NewServeMux()
	health.Register(mux, map[string]health.Check{"postgres": pool.Ping})
	system, err := systemChecks(pool)
	if err != nil {
		return err
	}
	health.RegisterSystem(mux, "/api/system", system)
	mux.Handle("GET /metrics", promhttp.Handler())
	ratePerSecond, err := strconv.Atoi(server.Env("RATE_LIMIT_PER_SECOND", "100"))
	if err != nil || ratePerSecond <= 0 {
		return fmt.Errorf("RATE_LIMIT_PER_SECOND must be a positive number")
	}
	httpapi.Register(mux, campaigns, provider, metrics, httpapi.Sending{RatePerSecond: ratePerSecond, MaxAttempts: delivery.DefaultConfig().MaxAttempts})
	if dir := os.Getenv("ASSET_DIR"); dir != "" {
		httpapi.RegisterAssets(mux, campaigns, asset.DiskStore{Dir: dir})
	}
	if secret := os.Getenv("UNSUBSCRIBE_SECRET"); secret != "" {
		httpapi.RegisterUnsubscribe(mux, campaigns, secret)
	}
	if secret := os.Getenv("PROVIDER_WEBHOOK_SECRET"); secret != "" {
		httpapi.RegisterProviderEvents(mux, campaigns, secret)
	}

	slog.Info("llm provider selected", "provider", provider.Name(), "model", provider.Model())
	return server.Run(ctx, ":"+server.Env("PORT", "8080"), telemetry.Middleware(metrics, logger, mux))
}

// systemChecks covers what a marketer depends on, including processes the API does
// not need itself. Anything not configured is left out rather than reported as down.
func systemChecks(pool *pgxpool.Pool) (map[string]health.Check, error) {
	checks := map[string]health.Check{"database": pool.Ping}
	if url := os.Getenv("REDIS_URL"); url != "" {
		options, err := redis.ParseURL(url)
		if err != nil {
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
		client := redis.NewClient(options)
		checks["redis"] = func(ctx context.Context) error { return client.Ping(ctx).Err() }
	}
	if url := os.Getenv("WORKER_HEALTH_URL"); url != "" {
		checks["worker"] = health.HTTPCheck(url)
	}
	if url := os.Getenv("PROVIDER_HEALTH_URL"); url != "" {
		checks["provider"] = health.HTTPCheck(url)
	}
	return checks, nil
}
