// Package server runs HTTP servers with graceful shutdown.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const shutdownTimeout = 10 * time.Second

// Run serves handler on addr until ctx is cancelled, then drains open requests.
func Run(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	failed := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", addr)
		failed <- srv.ListenAndServe()
	}()

	select {
	case err := <-failed:
		return fmt.Errorf("serve %s: %w", addr, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown %s: %w", addr, err)
	}
	return nil
}

// Env returns the environment variable or a fallback when it is unset.
func Env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// RequireEnv returns the environment variable or an error naming it.
func RequireEnv(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", fmt.Errorf("environment variable %s is required", key)
	}
	return value, nil
}
