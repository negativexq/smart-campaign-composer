// Command worker sends queued deliveries. It has its own health and metrics
// port because Redis and the delivery metrics belong to this process, not the API.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/negativexq/smart-campaign-composer/internal/db"
	"github.com/negativexq/smart-campaign-composer/internal/health"
	"github.com/negativexq/smart-campaign-composer/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker failed", "error", err)
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
	redisURL, err := server.RequireEnv("REDIS_URL")
	if err != nil {
		return err
	}

	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}
	redisClient := redis.NewClient(redisOptions)
	defer redisClient.Close()

	mux := http.NewServeMux()
	health.Register(mux, map[string]health.Check{
		"postgres": pool.Ping,
		"redis":    func(ctx context.Context) error { return redisClient.Ping(ctx).Err() },
	})
	mux.Handle("GET /metrics", promhttp.Handler())

	return server.Run(ctx, ":"+server.Env("OPS_PORT", "8080"), mux)
}
