// Package health serves liveness and readiness endpoints.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const checkTimeout = 2 * time.Second

// Check reports whether a dependency is usable.
type Check func(ctx context.Context) error

type readiness struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Register adds /healthz, which only says the process is serving, and /readyz,
// which verifies the dependencies this process itself needs. Failure details go
// to the log because connection errors can contain credentials.
func Register(mux *http.ServeMux, checks map[string]Check) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		result := run(r.Context(), checks)
		code := http.StatusOK
		if result.Status != "ok" {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, result)
	})
}

func run(ctx context.Context, checks map[string]Check) readiness {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	result := readiness{Status: "ok", Checks: make(map[string]string, len(checks))}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, check := range checks {
		wg.Go(func() {
			err := check(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				slog.Warn("readiness check failed", "check", name, "error", err)
				result.Status = "unavailable"
				result.Checks[name] = "down"
				return
			}
			result.Checks[name] = "ok"
		})
	}
	wg.Wait()
	return result
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("write health response", "error", err)
	}
}
