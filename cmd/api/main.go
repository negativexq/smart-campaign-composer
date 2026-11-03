// Command api serves the campaign REST and SSE endpoints.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/negativexq/smart-campaign-composer/internal/db"
	"github.com/negativexq/smart-campaign-composer/internal/health"
	"github.com/negativexq/smart-campaign-composer/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databaseURL, err := server.RequireEnv("DATABASE_URL")
	if err != nil {
		return err
	}
	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	mux := http.NewServeMux()
	health.Register(mux, map[string]health.Check{"postgres": pool.Ping})
	mux.Handle("GET /metrics", promhttp.Handler())

	return server.Run(ctx, ":"+server.Env("PORT", "8080"), mux)
}
