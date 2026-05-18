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

	"github.com/Singleton-Solution/GoNext/apps/api/internal/admin/rum"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/healthz"
	plugindev "github.com/Singleton-Solution/GoNext/apps/api/internal/plugins/dev"
	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/db"
	"github.com/Singleton-Solution/GoNext/packages/go/httpx"
	"github.com/Singleton-Solution/GoNext/packages/go/log"
	gonextmetrics "github.com/Singleton-Solution/GoNext/packages/go/metrics"
	"github.com/Singleton-Solution/GoNext/packages/go/middleware/earlyhints"
	httpmetrics "github.com/Singleton-Solution/GoNext/packages/go/middleware/metrics"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	redisclient "github.com/Singleton-Solution/GoNext/packages/go/redis"
	"github.com/Singleton-Solution/GoNext/packages/go/shutdown"
	"github.com/Singleton-Solution/GoNext/packages/go/theme/seed"
)

const serviceName = "api"

func main() {
	// --print-config is an operator-facing escape hatch: load the config
	// from the environment, dump it (secrets masked) to stderr, and exit 0
	// without ever opening the listener. Useful when a prod container is
	// crash-looping on boot and the operator needs to verify "what did
	// THIS process see" without attaching a debugger. We check os.Args
	// directly rather than reach for flag.Parse because the rest of main
	// doesn't take flags and we don't want to grow a surface for one
	// short-circuit.
	for _, a := range os.Args[1:] {
		if a == "--print-config" || a == "-print-config" {
			cfg, err := config.Load()
			if err != nil {
				// Print the error to stderr so operators see WHAT failed,
				// then dump whatever did load (Load returns a partial cfg
				// on error) so they can see what env vars were honored.
				fmt.Fprintf(os.Stderr, "config: %v\n", err)
			}
			if cfg != nil {
				if dumpErr := config.Dump(*cfg, os.Stderr); dumpErr != nil {
					fmt.Fprintf(os.Stderr, "dump: %v\n", dumpErr)
					os.Exit(1)
				}
			}
			if err != nil {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

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

	// First-boot theme seed. Runs after migrations have been applied
	// (which is the responsibility of either `gonext migrate up` or
	// the deploy pipeline — main does not run migrations itself).
	// EnsureDefault is idempotent: on every boot after the first it
	// observes the options row and returns immediately. We make this
	// a hard failure: a server that can't determine which theme is
	// active would render an empty page, and "empty page" is a more
	// confusing failure mode than "boot failed with a clear seed: ..."
	// message in the operator's log.
	//
	// Theme directory comes from GONEXT_THEME_DIR with the same
	// "./themes" default used by the CLI. It is intentionally NOT
	// promoted into config.Config yet — the option lives on a small
	// surface only the seeder consumes, and growing Config for one
	// path increases the blast radius of every future config change.
	themeDir := os.Getenv("GONEXT_THEME_DIR")
	if themeDir == "" {
		themeDir = "./themes"
	}
	if seedErr := (&seed.Seeder{
		DB:       seed.PoolQuerier{Pool: pool},
		ThemeDir: themeDir,
		SourceFS: seed.BundledThemes,
		Logger:   logger,
	}).EnsureDefault(ctx); seedErr != nil {
		return fmt.Errorf("theme seed: %w", seedErr)
	}

	// Metrics + audit are best-effort flush points. They're registered
	// AFTER persistence (so they drain BEFORE persistence on LIFO) —
	// the last audit record needs the DB pool alive when it writes.
	//
	// The metrics registry pre-registers go_* / process_* collectors
	// and owns the /metrics surface (issue #286). The HTTP metrics
	// middleware (issue #158) registers gonext_http_* against the same
	// registry so scrapers see them on the dedicated /metrics endpoint.
	//
	// Audit remains stubbed; that wiring lands in #54.
	metricsReg := gonextmetrics.NewRegistry()
	orch.MustRegister(logger, "metrics.flusher", noopCloser("metrics"))
	orch.MustRegister(logger, "audit.emitter", noopCloser("audit"))

	mux := buildRouter(cfg, pool, rdb, logger)

	// Build the middleware chain. Early Hints (issue #122) sits AFTER
	// Recovery (so a panicking hints provider doesn't crash the
	// server) but BEFORE Logger and metrics. The 103 we emit is about
	// the request, not its final 200, so we want it on the wire as
	// soon as possible — before any per-request bookkeeping work.
	//
	// When cfg.Performance.EarlyHints is false the middleware is
	// omitted from the chain entirely rather than being a runtime
	// no-op; the disabled path costs zero per-request overhead this
	// way. Operators flip GONEXT_PERFORMANCE_EARLY_HINTS=false to
	// disable without recompiling.
	mws := []httpx.Middleware{
		httpx.Recovery(logger),
	}
	if cfg.Performance.EarlyHints {
		// Static provider seeded from the seeded theme's known URL.
		// Subsequent wiring (issue #11 follow-ups) replaces this with
		// a ThemeAwareProvider backed by the live theme store. For
		// now the static map covers the common /index + /blog roots
		// served by the seeded theme.
		hintsProvider := earlyhints.NewStaticProvider(map[string][]earlyhints.Hint{
			"/": {
				{
					URL:           "/themes/active/style.css",
					As:            "style",
					FetchPriority: "high",
				},
			},
		})
		mws = append(mws, earlyhints.Middleware(hintsProvider, earlyhints.Options{
			Logger: logger,
		}))
	}
	mws = append(mws,
		httpx.RequestID(),
		httpx.Logger(logger, "/healthz", "/readyz"),
		httpmetrics.Middleware(metricsReg.Prometheus()),
	)

	srv, err := httpx.New(httpx.Options{
		Config:      cfg.Server,
		Log:         logger,
		Handler:     mux,
		Middlewares: mws,
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
//
// The dev-install plugin endpoint (/_/plugins/dev/install) is mounted
// only when cfg.Plugins.DevMode is true. Production deployments never
// enable DevMode, so they never see this surface — registering the
// route conditionally (rather than gating at request time) is the
// strongest guarantee we can offer.
func buildRouter(cfg *config.Config, pool *pgxpool.Pool, rdb *goredis.Client, logger *slog.Logger) http.Handler {
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

	// In-house RUM (issue #132). The beacon endpoint is mounted
	// unconditionally — the public theme respects cfg.RUM.Enabled
	// before it emits scripts, and an off-by-default flag at the
	// theme layer is the right level for this knob. The read
	// endpoints are policy-gated and inherit the same wiring as
	// the other admin surfaces.
	if err := rum.Mount(mux, "/_/rum/beacon", "/api/v1/admin/rum", rum.Deps{
		Store:  rum.NewMemoryStore(),
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Logger: logger,
	}); err != nil {
		logger.Warn("rum: failed to mount routes", slog.Any("err", err))
	} else {
		logger.Info("rum: routes mounted",
			slog.String("beacon", "/_/rum/beacon"),
			slog.String("read", "/api/v1/admin/rum"),
		)
	}

	if cfg.Plugins.DevMode {
		// Dev plugin install endpoint. Used by the `gonext plugin dev`
		// CLI's watch loop. Storage is intentionally in-memory: the
		// dev surface is for hot-reloading a single plugin while you
		// iterate; persistence across api restarts isn't part of the
		// contract. The audit emitter writes to an in-memory store so
		// nothing in the dev path touches the prod audit DB schema
		// before #54 ships.
		mgr := lifecycle.NewManager(
			lifecycle.NewMemoryStorage(),
			audit.NewEmitter(audit.NewMemoryStore()),
			lifecycle.WithLogger(logger),
		)
		mux.Handle("POST /_/plugins/dev/install", plugindev.Mount(cfg.Plugins, mgr, plugindev.WithLogger(logger)))
		logger.Info("plugins/dev: install endpoint mounted",
			slog.String("path", "/_/plugins/dev/install"),
		)
	}

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
