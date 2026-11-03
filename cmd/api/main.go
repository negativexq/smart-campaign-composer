// Command api serves the campaign REST and SSE endpoints.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/db"
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
	mux.Handle("GET /metrics", promhttp.Handler())
	httpapi.Register(mux, campaigns, provider)

	slog.Info("llm provider selected", "provider", provider.Name(), "model", provider.Model())
	return server.Run(ctx, ":"+server.Env("PORT", "8080"), telemetry.Middleware(metrics, logger, mux))
}
