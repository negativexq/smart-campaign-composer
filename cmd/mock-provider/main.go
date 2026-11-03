// Command mock-provider imitates a real SMS and email gateway.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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

	mux := http.NewServeMux()
	health.Register(mux, nil)
	behavior := behaviorFromEnv()
	if url := os.Getenv("PROVIDER_EVENTS_URL"); url != "" {
		behavior.OnEvent = mockprovider.NewWebhook(url, os.Getenv("PROVIDER_WEBHOOK_SECRET"), time.Second)
	}
	mockprovider.New(behavior).Register(mux)

	if err := server.Run(ctx, ":"+server.Env("PORT", "8080"), mux); err != nil {
		slog.Error("mock provider failed", "error", err)
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
	return behavior
}
