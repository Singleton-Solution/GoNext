package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler returns an http.Handler that serves the registry's metrics in
// the Prometheus text exposition format. Mount it at /metrics:
//
//	mux := http.NewServeMux()
//	mux.Handle("GET /metrics", reg.Handler())
//
// Most callers should use ServeMetrics instead, which binds /metrics on
// a dedicated port to isolate scrape traffic from public requests.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{
		// Don't leak the registry's internal errors to scrapers.
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// metricsServerTimeouts are conservative defaults for the dedicated metrics
// listener. Scrapes are cheap and short — long timeouts only invite slow
// loris on what is meant to be an internal port.
const (
	metricsReadHeaderTimeout = 5 * time.Second
	metricsReadTimeout       = 10 * time.Second
	metricsWriteTimeout      = 10 * time.Second
	metricsIdleTimeout       = 30 * time.Second
	metricsShutdownTimeout   = 5 * time.Second
)

// ServeMetrics starts a dedicated HTTP server bound to addr (typically
// ":9090") that serves /metrics from this registry.
//
// Why a separate listener: scrape traffic should not flow through the
// public API listener. A separate port lets operators put NetworkPolicy /
// firewall rules in front of it without affecting user traffic, and
// keeps scrape latency out of public-listener histograms.
//
// Returns:
//
//   - the *http.Server (already serving in a goroutine; nil if the listen
//     fails),
//   - a shutdown func that gracefully drains within metricsShutdownTimeout,
//   - an error if the initial Listen fails (e.g. port already in use).
//
// The server logs bind and shutdown events via logger. logger is required;
// pass slog.New(slog.NewJSONHandler(io.Discard, nil)) if you truly don't
// want output (tests).
//
// Calling the returned shutdown func is safe and idempotent. It does NOT
// return until the drain completes or the budget expires.
func (r *Registry) ServeMetrics(addr string, logger *slog.Logger) (*http.Server, func() error, error) {
	if logger == nil {
		return nil, nil, errors.New("metrics.ServeMetrics: logger is required")
	}
	if addr == "" {
		return nil, nil, errors.New("metrics.ServeMetrics: addr is required")
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("metrics.ServeMetrics: listen %q: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", r.Handler())
	// A bare-bones liveness for the metrics listener itself, so a probe
	// against :9090 doesn't have to hit /metrics (which can be heavy).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	bound := ln.Addr().String()
	srv := &http.Server{
		// Set Addr to the bound address so callers can read it after
		// ServeMetrics returns — useful for tests with ":0" and for
		// operators correlating the log line below with the server.
		Addr:              bound,
		Handler:           mux,
		ReadHeaderTimeout: metricsReadHeaderTimeout,
		ReadTimeout:       metricsReadTimeout,
		WriteTimeout:      metricsWriteTimeout,
		IdleTimeout:       metricsIdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	logger.Info("metrics server listening",
		slog.String("addr", bound),
		slog.String("path", "/metrics"),
	)

	go func() {
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server exited", slog.String("err", err.Error()))
		}
	}()

	shutdown := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), metricsShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Warn("metrics server shutdown exceeded budget",
				slog.Duration("budget", metricsShutdownTimeout),
				slog.String("err", err.Error()),
			)
			return fmt.Errorf("metrics.ServeMetrics shutdown: %w", err)
		}
		logger.Info("metrics server stopped", slog.String("addr", bound))
		return nil
	}

	return srv, shutdown, nil
}
