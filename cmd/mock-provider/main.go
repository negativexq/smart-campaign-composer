// Command mock-provider imitates a real SMS and email gateway.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/health"
	"github.com/negativexq/smart-campaign-composer/internal/mockprovider"
	"github.com/negativexq/smart-campaign-composer/internal/server"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

func main() {
	slog.SetDefault(telemetry.NewLogger(os.Stdout))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := telemetry.SetupTracing(ctx, "mock-provider", os.Getenv)
	if err != nil {
		slog.Error("set up tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			slog.Warn("flush traces", "error", err)
		}
	}()
	telemetry.StartProfiling(ctx, os.Getenv("PPROF_ADDR"))

	mux := http.NewServeMux()
	health.Register(mux, nil)
	behavior := behaviorFromEnv()
	if url := os.Getenv("PROVIDER_EVENTS_URL"); url != "" {
		behavior.OnEvent = mockprovider.NewWebhook(url, os.Getenv("PROVIDER_WEBHOOK_SECRET"))
	}
	provider, err := mockprovider.Open(behavior, os.Getenv("MOCK_JOURNAL_FILE"))
	if err != nil {
		slog.Error("mock provider cannot read its journal", "error", err)
		os.Exit(1)
	}
	provider.Register(mux)

	runErr := server.Run(ctx, ":"+server.Env("PORT", "8080"), mux)
	if err := provider.Close(); err != nil {
		slog.Error("close journal", "error", err)
	}
	if runErr != nil {
		slog.Error("mock provider failed", "error", runErr)
		os.Exit(1)
	}
}

// behaviorFromEnv lets a demo turn the trouble up or off.
func behaviorFromEnv() mockprovider.Behavior {
	behavior := mockprovider.DefaultBehavior()
	for name, target := range map[string]*float64{
		"MOCK_RATE_LIMIT_RATE":    &behavior.RateLimitRate,
		"MOCK_SERVER_ERROR_RATE":  &behavior.ServerErrorRate,
		"MOCK_LOST_RESPONSE_RATE": &behavior.LostResponseRate,
		"MOCK_BOUNCE_RATE":        &behavior.BounceRate,
		"MOCK_SLOW_RATE":          &behavior.SlowRate,
	} {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		rate, err := strconv.ParseFloat(value, 64)
		if err != nil {
			slog.Warn("ignoring invalid setting", "name", name, "value", value)
			continue
		}
		*target = rate
	}
	for name, target := range map[string]*time.Duration{
		"MOCK_MIN_DELAY_MS": &behavior.MinDelay,
		"MOCK_MAX_DELAY_MS": &behavior.MaxDelay,
		"MOCK_SLOW_MIN_MS":  &behavior.SlowMinDelay,
		"MOCK_SLOW_MAX_MS":  &behavior.SlowMaxDelay,
	} {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		milliseconds, err := strconv.Atoi(value)
		if err != nil || milliseconds < 0 {
			slog.Warn("ignoring invalid setting", "name", name, "value", value)
			continue
		}
		*target = time.Duration(milliseconds) * time.Millisecond
	}
	if value := os.Getenv("MOCK_UNAUTHORIZED_CHANNELS"); value != "" {
		behavior.Unauthorized = strings.Split(value, ",")
	}
	return behavior
}
