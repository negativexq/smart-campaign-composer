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
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"

	"github.com/negativexq/smart-campaign-composer/internal/db"
	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/health"
	"github.com/negativexq/smart-campaign-composer/internal/server"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

func main() {
	slog.SetDefault(telemetry.NewLogger(os.Stdout))
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

	providerURL, err := server.RequireEnv("PROVIDER_URL")
	if err != nil {
		return err
	}
	ratePerSecond, err := strconv.Atoi(server.Env("RATE_LIMIT_PER_SECOND", "100"))
	if err != nil || ratePerSecond <= 0 {
		return fmt.Errorf("RATE_LIMIT_PER_SECOND must be a positive number")
	}
	shutdownTracing, err := telemetry.SetupTracing(ctx, "worker", os.Getenv)
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

	worker := delivery.New(pool, delivery.NewHTTPSender(providerURL),
		delivery.NewRedisLimiter(redisClient, ratePerSecond, "delivery"),
		telemetry.NewMetrics(prometheus.DefaultRegisterer), otel.Tracer("delivery"), delivery.DefaultConfig())

	var running sync.WaitGroup
	running.Go(func() {
		if err := worker.Run(ctx); err != nil {
			slog.Error("delivery loop stopped", "error", err)
		}
	})
	defer running.Wait()

	return server.Run(ctx, ":"+server.Env("OPS_PORT", "8080"), mux)
}
