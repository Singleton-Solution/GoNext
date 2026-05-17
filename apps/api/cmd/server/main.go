// Command server is the GoNext HTTP API server.
//
// It loads configuration from the environment, sets up structured logging,
// builds the HTTP router, applies the middleware chain, and serves until
// interrupted by SIGINT/SIGTERM (the standard container lifecycle) or
// until the parent context is canceled. On signal, the shutdown
// orchestrator (packages/go/shutdown) drives a LIFO drain across the
// HTTP server, audit emitter, metrics flusher, Redis client, and DB
// pool — all within the configured ShutdownTimeout budget.
//
// Exit codes:
//
//	0 — clean shutdown after every registered closer drained.
//	1 — configuration or startup error before serving began.
//	2 — server error during run (port conflict, drain timeout, etc.).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/healthz"
	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/db"
	"github.com/Singleton-Solution/GoNext/packages/go/httpx"
	"github.com/Singleton-Solution/GoNext/packages/go/log"
	redisclient "github.com/Singleton-Solution/GoNext/packages/go/redis"
	"github.com/Singleton-Solution/GoNext/packages/go/shutdown"
)

const serviceName = "api"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(2)
	}
}

// run is main() with a returned error, so it's testable and unit-tests
// don't need to swap os.Exit.
func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	bi := buildinfo.Get(serviceName)
	logger, err := log.Setup(os.Stdout, log.Options{
		Service:   serviceName,
		Version:   bi.Version,
		Commit:    bi.Commit,
		Level:     parseLogLevel(cfg.Log.Level),
		Format:    log.Format(cfg.Log.Format),
		AddSource: cfg.Log.AddSource,
		Redact:    true,
	})
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}

	logger.Info("starting",
		"env", string(cfg.Env),
		"addr", cfg.Server.Addr,
		"go_version", bi.GoVersion,
	)

	// Build the shutdown orchestrator first so we can register every
	// long-lived resource as we create it. The orchestrator owns the
	// signal handler (SIGINT/SIGTERM) and the drain budget; the
	// per-binary main() is responsible only for wiring resources in
	// dependency order.
	orch, err := shutdown.New(shutdown.Options{
		Log:    logger,
		Budget: cfg.Server.ShutdownTimeout,
	})
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	// DB and Redis are persistent state — register them first so they
	// drain LAST (LIFO). Everything that runs during the drain (the
	// HTTP server flushing in-flight responses, the audit emitter
	// writing its last record) may depend on these.
	pool, err := db.New(ctx, cfg.Database, logger)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if regErr := orch.Register("db.pool", shutdown.CloserFromFunc(pool.Close)); regErr != nil {
		pool.Close()
		return fmt.Errorf("register db: %w", regErr)
	}

	rdb, err := redisclient.New(ctx, cfg.Redis, logger)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	if regErr := orch.Register("redis.client", shutdown.CloserFromIO(rdb)); regErr != nil {
		_ = rdb.Close()
		return fmt.Errorf("register redis: %w", regErr)
	}

	// Metrics + audit are best-effort flush points. They're registered
	// AFTER persistence (so they drain BEFORE persistence on LIFO) —
	// the last audit record needs the DB pool alive when it writes.
	//
	// Stubbed for now: metrics.Close and audit.Close exist in their
	// respective packages but the wiring of a real exporter/emitter
	// lands in the per-issue work for #4 (metrics) and #54 (audit).
	// The registration shape is final, so when those exporters land,
	// the only diff here is swapping the no-op for the real instance.
	orch.MustRegister(logger, "metrics.flusher", noopCloser("metrics"))
	orch.MustRegister(logger, "audit.emitter", noopCloser("audit"))

	mux := buildRouter(cfg, pool, rdb)

	srv, err := httpx.New(httpx.Options{
		Config:  cfg.Server,
		Log:     logger,
		Handler: mux,
		Middlewares: []httpx.Middleware{
			httpx.Recovery(logger),
			httpx.RequestID(),
			httpx.Logger(logger, "/healthz", "/readyz"),
		},
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	// The HTTP server is the LAST registration → FIRST to drain. That's
	// the contract: stop accepting new connections immediately, then
	// drain in-flight, then unwind state. We run Run() in a goroutine
	// with a derived ctx so the orchestrator can cancel it just before
	// invoking Shutdown.
	serverCtx, cancelServer := context.WithCancel(ctx)
	defer cancelServer()
	orch.MustRegister(logger, "http.server", func(shutdownCtx context.Context) error {
		// Cancel Run's ctx first so its own signal/ctx wait unblocks;
		// then call Shutdown to drain in-flight requests. Without the
		// cancel, Run's goroutine would Shutdown twice (once via its
		// own signal handler if a real SIGTERM raced, once via us).
		cancelServer()
		return srv.Shutdown(shutdownCtx)
	})

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(serverCtx) }()

	// Wait for either a signal/ctx cancellation (orchestrator path) or
	// the server's Run returning an error on its own (port conflict,
	// bind failure). Either way, drain through the orchestrator so all
	// resources see a consistent shutdown.
	waitErr := make(chan error, 1)
	go func() { waitErr <- orch.Wait(ctx) }()

	select {
	case err := <-runErr:
		// Server crashed (or stopped cleanly before signal). Run the
		// drain anyway so the DB pool and Redis client are closed
		// cleanly.
		drainErr := orch.Drain(context.Background())
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server: %w", err)
		}
		return drainErr
	case err := <-waitErr:
		// Drain finished. The Run goroutine has been canceled and its
		// closer fired through the orchestrator; drain its return
		// value with a non-blocking receive so it isn't leaked.
		select {
		case <-runErr:
		default:
		}
		return err
	}
}

// buildRouter assembles the HTTP route table. Subsequent issues mount
// real routes here; for now we serve a single root endpoint that returns
// the binary's identity, as required by issue #2's AC, plus the
// operational health endpoints introduced in #8.
//
// pool and rdb are threaded in so the readiness handler can probe them.
// They are NOT consulted for liveness — liveness must never depend on
// anything external (see internal/healthz/doc.go).
func buildRouter(_ *config.Config, pool *pgxpool.Pool, rdb *goredis.Client) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		bi := buildinfo.Get(serviceName)
		writeJSON(w, http.StatusOK, map[string]any{
			"name":    "gonext",
			"version": bi.Version,
			"commit":  bi.Commit,
		})
	})

	// Operational health endpoints. Both are excluded from the Logger
	// middleware's per-request log line (see httpx.Logger skipPaths in
	// run()) so Kubernetes probe traffic doesn't drown the request log.
	mux.Handle("GET /healthz", healthz.Liveness())
	mux.Handle("GET /readyz", healthz.Readiness(
		healthz.DBCheck(pool),
		healthz.RedisCheck(rdb),
	))

	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// parseLogLevel maps the config string to slog.Level. Unknown values
// fall back to INFO; log.OptionsFromEnv applies the same rule, but
// since main.go uses log.Setup directly (with explicit Options) we
// repeat the mapping here.
func parseLogLevel(s string) slog.Level {
	switch s {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// noopCloser is a placeholder for resources whose Close hooks aren't
// implemented yet (audit emitter, metrics flusher). Logging the name
// lets us prove the wiring is correct even before the real flushers
// arrive. Once the real implementations land in their respective
// issues, this helper goes away.
func noopCloser(name string) shutdown.Closer {
	return func(_ context.Context) error {
		// Intentionally silent — the orchestrator already logs every
		// step with its duration. A no-op closer adds no useful
		// information beyond "I was registered correctly".
		_ = name
		return nil
	}
}
