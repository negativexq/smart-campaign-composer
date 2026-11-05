package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	// blockProfileRate samples one blocking event per this many nanoseconds spent blocked.
	blockProfileRate = int(time.Millisecond)
	// mutexProfileFraction samples one in this many contended locks.
	mutexProfileFraction = 100
	profilingStopTimeout = 5 * time.Second
)

// StartProfiling serves Go's profiles on their own address, apart from the API and
// its metrics. It does nothing when addr is empty, which is the default: profiles
// show how the program works inside and are for test environments.
func StartProfiling(ctx context.Context, addr string) {
	if addr == "" {
		return
	}
	runtime.SetBlockProfileRate(blockProfileRate)
	runtime.SetMutexProfileFraction(mutexProfileFraction)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: profilingStopTimeout}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("profiling server stopped", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), profilingStopTimeout)
		defer cancel()
		_ = server.Shutdown(stopCtx)
	}()
}

// RegisterPool publishes a connection pool's own counters, which show whether
// queries wait for a free connection.
func RegisterPool(registerer prometheus.Registerer, pool *pgxpool.Pool) {
	factory := promauto.With(registerer)
	factory.NewCounterFunc(prometheus.CounterOpts{
		Name: "db_pool_acquire_total",
		Help: "Connections taken from the pool.",
	}, func() float64 { return float64(pool.Stat().AcquireCount()) })
	factory.NewCounterFunc(prometheus.CounterOpts{
		Name: "db_pool_acquire_seconds_total",
		Help: "Time spent taking connections from the pool.",
	}, func() float64 { return pool.Stat().AcquireDuration().Seconds() })
	factory.NewCounterFunc(prometheus.CounterOpts{
		Name: "db_pool_empty_acquire_total",
		Help: "Times a caller had to wait because no connection was free.",
	}, func() float64 { return float64(pool.Stat().EmptyAcquireCount()) })
	factory.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "db_pool_acquired_conns",
		Help: "Connections in use.",
	}, func() float64 { return float64(pool.Stat().AcquiredConns()) })
	factory.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "db_pool_max_conns",
		Help: "Largest size the pool can reach.",
	}, func() float64 { return float64(pool.Stat().MaxConns()) })
}
