// Command mock-provider imitates a real SMS and email gateway.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/negativexq/smart-campaign-composer/internal/health"
	"github.com/negativexq/smart-campaign-composer/internal/server"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

func main() {
	slog.SetDefault(telemetry.NewLogger(os.Stdout))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	health.Register(mux, nil)

	if err := server.Run(ctx, ":"+server.Env("PORT", "8080"), mux); err != nil {
		slog.Error("mock provider failed", "error", err)
		os.Exit(1)
	}
}
