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
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	admincomments "github.com/Singleton-Solution/GoNext/apps/api/internal/admin/comments"
	adminmedia "github.com/Singleton-Solution/GoNext/apps/api/internal/admin/media"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/admin/customizer"
	adminredirects "github.com/Singleton-Solution/GoNext/apps/api/internal/admin/redirects"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/admin/rum"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/auth/login"
	authsessions "github.com/Singleton-Solution/GoNext/apps/api/internal/auth/sessions"
	authverify "github.com/Singleton-Solution/GoNext/apps/api/internal/auth/verify"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/healthz"
	restcomments "github.com/Singleton-Solution/GoNext/apps/api/internal/rest/comments"
	restposts "github.com/Singleton-Solution/GoNext/apps/api/internal/rest/posts"
	restsearch "github.com/Singleton-Solution/GoNext/apps/api/internal/rest/search"
	openapidocs "github.com/Singleton-Solution/GoNext/apps/api/internal/openapi"
	plugindev "github.com/Singleton-Solution/GoNext/apps/api/internal/plugins/dev"
	"github.com/Singleton-Solution/GoNext/apps/api/internal/setup"
	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/Singleton-Solution/GoNext/packages/go/db"
	"github.com/Singleton-Solution/GoNext/packages/go/email"
	"github.com/Singleton-Solution/GoNext/packages/go/httpx"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/log"
	gonextmetrics "github.com/Singleton-Solution/GoNext/packages/go/metrics"
	authmw "github.com/Singleton-Solution/GoNext/packages/go/middleware/auth"
	"github.com/Singleton-Solution/GoNext/packages/go/middleware/earlyhints"
	httpmetrics "github.com/Singleton-Solution/GoNext/packages/go/middleware/metrics"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/policy"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
	redisclient "github.com/Singleton-Solution/GoNext/packages/go/redis"
	"github.com/Singleton-Solution/GoNext/packages/go/redirects"
	pkgsearch "github.com/Singleton-Solution/GoNext/packages/go/search"
	"github.com/Singleton-Solution/GoNext/packages/go/session"
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

	// Session manager. Built here (rather than inside buildRouter) so its
	// Close hook can be registered with the shutdown orchestrator and so
	// every handler that mints sessions (login, setup, future password
	// reset) shares the same instance.
	sessions := session.NewWithClient(rdb, logger)

	// Redirect rules engine (issue: WordPress-parity 301 admin). The
	// engine consults a snapshot of the redirect_rules table and serves
	// 3xx responses for matched paths BEFORE the renderer mux ever
	// sees them — a request for /old-page returning 301 must never
	// also incur a renderer DB lookup that would 404 anyway. The
	// flusher goroutine batches hit-count writes every 30s so the
	// hot path stays lock-free.
	//
	// Reload errors are non-fatal at boot: an empty engine serves
	// no redirects, which is the same posture as a fresh install.
	redirectStore := redirects.NewPgxStore(pool)
	redirectEngine := redirects.NewEngine(redirectStore)
	if reloadErr := redirectEngine.Reload(ctx); reloadErr != nil {
		logger.Warn("redirects: initial reload failed; serving empty rule set", "err", reloadErr)
	}
	redirectEngine.Start()
	orch.MustRegister(logger, "redirects.engine", func(stopCtx context.Context) error {
		return redirectEngine.Stop(stopCtx)
	})

	mux := buildRouter(cfg, pool, rdb, sessions, themeDir, logger, redirectStore, redirectEngine)

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
		// Redirect rules sit AFTER request-id / logger (so a 301
		// shows up in the access log with the rest of the request
		// trail) but BEFORE the mux: a matched rule short-circuits
		// the renderer entirely.
		redirects.Middleware(redirectEngine),
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
func buildRouter(cfg *config.Config, pool *pgxpool.Pool, rdb *goredis.Client, sessions *session.Manager, themeDir string, logger *slog.Logger, redirectStore redirects.Store, redirectEngine *redirects.Engine) http.Handler {
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

	// OpenAPI surface (issue #310). The JSON form is the production
	// canonical (every SDK generator targets it); the YAML form is the
	// human-friendly diff target. Both are public — no auth — because
	// the document describes the contract, not the data.
	//
	// The Swagger UI page is gated on non-prod environments by the
	// caller; in this binary we mount it unconditionally on dev so a
	// `make run` developer can poke the API in a browser without env
	// gymnastics. Production deploys keep the JSON+YAML and drop the UI
	// by overriding the route table.
	mux.Handle("GET /openapi.json", openapidocs.Handler())
	mux.Handle("GET /api/openapi.yaml", openapidocs.YAMLHandler())
	mux.Handle("GET /docs/", openapidocs.SwaggerUIHandler())

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

	// Media library admin (issue: media library). Uses the in-memory
	// store + putter at this tier — the Postgres-backed store and the
	// minio-go ObjectPutter land in a follow-up wiring change once the
	// config-level storage client is shared between the upload path and
	// the public variant proxy. The in-memory wiring keeps the admin UI
	// functional end-to-end for smoke tests without requiring a MinIO
	// container for `make dev`.
	if err := adminmedia.Mount(mux, "/api/v1/admin/media", adminmedia.Deps{
		Store:  adminmedia.NewMemoryStore(nil, nil),
		Putter: adminmedia.NewMemoryPutter(),
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Logger: logger,
	}); err != nil {
		logger.Warn("admin/media: failed to mount routes", slog.Any("err", err))
	} else {
		logger.Info("admin/media: routes mounted",
			slog.String("base", "/api/v1/admin/media"),
		)
	}

	// Redirect rules admin (WordPress-parity 301 administration). The
	// engine is wired into the middleware chain ABOVE this router,
	// so a matched rule never reaches the mux. The admin handlers
	// here let operators create/edit/delete rules and call Reload on
	// the engine when they mutate state.
	if err := adminredirects.Mount(mux, "/api/v1/admin/redirects", adminredirects.Deps{
		Store:  redirectStore,
		Engine: redirectEngine,
		Logger: logger,
	}); err != nil {
		logger.Warn("admin/redirects: failed to mount routes", slog.Any("err", err))
	} else {
		logger.Info("admin/redirects: routes mounted",
			slog.String("base", "/api/v1/admin/redirects"),
		)
	}

	// Comments admin moderation surface. Mounts the list/update/bulk/
	// reply routes under /api/v1/admin/comments. The store is the
	// Postgres-backed implementation against the comments table from
	// migration 000006 — ltree threading, bulk transactions, and the
	// joined post + author fields all live in pgx_store.go. Falls
	// back to the in-memory store when the pool is nil so the dev
	// loop keeps working without a database. Policy-gated by the
	// moderate_comments capability check inside Mount.
	var adminCommentsStore admincomments.Store
	if pool != nil {
		adminCommentsStore = admincomments.NewPgxStore(pool)
	} else {
		adminCommentsStore = admincomments.NewMemoryStore()
		logger.Warn("admin/comments: pool nil; using in-memory store")
	}
	if err := admincomments.Mount(mux, "/api/v1/admin/comments", admincomments.Deps{
		Store:  adminCommentsStore,
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Logger: logger,
	}); err != nil {
		logger.Warn("admin/comments: failed to mount routes", slog.Any("err", err))
	} else {
		logger.Info("admin/comments: routes mounted",
			slog.String("base", "/api/v1/admin/comments"),
		)
	}

	// Theme Customizer (issue #355). Operators GET the active theme +
	// any persisted overrides and PUT a partial-override payload that
	// the renderer merges at request time. The route prefix mirrors
	// the rest of the admin REST surface so operators looking for
	// "admin/customizer" find it next to "admin/jobs", "admin/rum",
	// "admin/status".
	if err := customizer.Mount(mux, "/api/v1/admin/customizer", customizer.Deps{
		Store:  customizer.NewPgxStore(customizer.PoolAdapter{Pool: pool}),
		Loader: customizer.FilesystemLoader(themeDir),
		Policy: policy.NewBasicPolicy(policy.DefaultRoleCapabilities()),
		Logger: logger,
	}); err != nil {
		logger.Warn("customizer: failed to mount routes", slog.Any("err", err))
	} else {
		logger.Info("customizer: routes mounted",
			slog.String("base", "/api/v1/admin/customizer"),
		)
	}

	// First-run install surface — the in-browser alternative to
	// `gonext init`. The two endpoints (/api/v1/setup/status and
	// /api/v1/setup/install) are mounted unconditionally; the lock
	// behavior is enforced inside the package once
	// core.site.installation_completed_at exists in the options table.
	//
	// We use a local in-process rate-limiter rather than the Redis-backed
	// one: install attempts are rare (a single-digit number across the
	// lifetime of a fresh deployment), and an attacker who can bypass the
	// process-local bucket by hitting a different replica is bounded by
	// the global install lock anyway. The simpler wiring is the right
	// trade.
	setupLimiter, err := setup.NewMemoryLimiter(setup.DefaultRateLimit)
	if err != nil {
		logger.Warn("setup: failed to construct rate limiter", slog.Any("err", err))
	} else if mErr := setup.Mount(mux, setup.Deps{
		Users:              setup.NewPgUserCreator(setup.PoolAdapter{Pool: pool}),
		Options:            setup.NewPgOptionStore(setup.PoolAdapter{Pool: pool}),
		Sessions:           sessions,
		Hash:               password.Hash,
		Pepper:             []byte(cfg.Auth.Pepper),
		Limiter:            setupLimiter,
		SessionAbsoluteTTL: cfg.Auth.SessionTTL,
		SessionIdleTTL:     cfg.Auth.SessionIdleTTL,
		Insecure:           cfg.Env != "production",
		Log:                logger,
	}); mErr != nil {
		logger.Warn("setup: failed to mount routes", slog.Any("err", mErr))
	} else {
		logger.Info("setup: routes mounted",
			slog.String("status", "/api/v1/setup/status"),
			slog.String("install", "/api/v1/setup/install"),
		)
	}

	// Public comments REST surface (this issue). Anonymous- and
	// logged-in-friendly. Mounts at /api/v1/posts/{id}/comments. The
	// store is the package's Postgres-backed implementation against
	// the comments table from migration 000006 — the ltree path is
	// materialised by the BEFORE-INSERT trigger so the application
	// code just hands the row in. Falls back to the in-memory store
	// when the pool is nil (no-DB development), preserving the
	// existing dev-loop behaviour. CORS allows the Email.SiteURL
	// origin (the canonical public-site URL today; promoted to a
	// dedicated PublicSite.BaseURL once that field graduates from
	// #190 to config.Config).
	var commentsStore restcomments.Store
	if pool != nil {
		commentsStore = restcomments.NewPgxStore(pool)
	} else {
		commentsStore = restcomments.NewMemoryStore()
		logger.Warn("rest/comments: pool nil; using in-memory store")
	}
	if err := restcomments.Mount(mux, "/api/v1/posts", restcomments.Deps{
		Store:       commentsStore,
		Logger:      logger,
		AllowOrigin: cfg.Email.SiteURL,
	}); err != nil {
		logger.Warn("rest/comments: failed to mount routes", slog.Any("err", err))
	} else {
		logger.Info("rest/comments: routes mounted",
			slog.String("base", "/api/v1/posts"),
		)
	}

	// -----------------------------------------------------------------
	// Auth + REST surface wiring (K3 verify finding, issues #424/#427).
	//
	// The five blocks below mount route packages whose handlers already
	// exist but were never reachable because main.go forgot to call
	// their Mount/Routes helpers. Without these the admin UI renders
	// but POST /api/v1/auth/login 404s — i.e. no user can sign in.
	//
	// Persistence here is the same trade-off the rest of buildRouter
	// makes today: pgxpool-backed adapters where the SQL already exists
	// (login.UserLookup, verify.PgxUserVerifier), in-memory stores for
	// surfaces whose pgx DAO is still in flight (posts). Migrating
	// posts to PgStore lands in the same follow-up that wires the
	// shared DAO across the renderer and admin paths.
	// -----------------------------------------------------------------

	// Login (POST /api/v1/auth/login). Two-bucket limiter (per-IP +
	// per-email) backed by memory in this binary; production deploys
	// will swap NewMemoryLimiter -> NewRedisLimiter once the auth
	// limiter has dedicated Redis prefixes carved out. TOTPLookup /
	// Rehash / Intermediate are deliberately nil — 2FA is gated by a
	// follow-up (no enrolment table yet), so leaving them nil is the
	// right shape: login.Deps.validate() accepts that combination.
	ipLoginLim, ipErr := ratelimit.NewMemoryLimiter(ratelimit.Policy{
		Capacity:   20,
		RefillRate: 20.0 / (5 * 60),
	})
	emailLoginLim, emailErr := ratelimit.NewMemoryLimiter(ratelimit.Policy{
		Capacity:   5,
		RefillRate: 5.0 / (15 * 60),
	})
	if sessions == nil {
		logger.Warn("login: skipping mount; session manager is nil")
	} else if ipErr != nil || emailErr != nil {
		logger.Warn("login: failed to build rate limiters",
			slog.Any("ip_err", ipErr),
			slog.Any("email_err", emailErr))
	} else {
		loginLimiter, lalErr := ratelimit.NewLoginAttemptLimiter(ratelimit.LoginAttemptOptions{
			IPLimiter:    &ratelimit.IPLimiter{Limiter: ipLoginLim},
			EmailLimiter: emailLoginLim,
		})
		if lalErr != nil {
			logger.Warn("login: failed to build login limiter", slog.Any("err", lalErr))
		} else {
			loginAudit := audit.NewEmitter(audit.NewMemoryStore())
			if err := login.Mount(mux, login.Deps{
				Lookup:             userLookupByEmail(pool),
				Sessions:           sessions,
				Pepper:             []byte(cfg.Auth.Pepper),
				SessionAbsoluteTTL: cfg.Auth.SessionTTL,
				SessionIdleTTL:     cfg.Auth.SessionIdleTTL,
				Limiter:            loginLimiter,
				AuditEmitter:       loginAudit,
				Insecure:           cfg.Env != "production",
				Log:                logger,
			}); err != nil {
				logger.Warn("login: failed to mount", slog.Any("err", err))
			} else {
				logger.Info("login: routes mounted", slog.String("base", "/api/v1/auth/login"))
			}
		}
	}

	// Sessions API (GET/DELETE /api/v1/auth/sessions[/{id}]). Mounted
	// behind RequireSession — these endpoints are useless to an
	// anonymous caller and rejecting at the middleware layer keeps the
	// "is the cookie valid" check off the per-handler path.
	if sessions != nil {
		sessionsHandlers := authsessions.NewHandlers(
			sessions,
			audit.NewEmitter(audit.NewMemoryStore()),
			authsessions.WithLogger(logger),
		)
		guarded := authmw.RequireSession(sessions)(sessionsHandlers.Routes())
		// http.ServeMux's "PATH" pattern matches both /sessions and
		// /sessions/{id}; we add the explicit /{id} pattern so the
		// sub-mux receives the trailing-path forms cleanly.
		mux.Handle("/api/v1/auth/sessions", guarded)
		mux.Handle("/api/v1/auth/sessions/", guarded)
		logger.Info("auth/sessions: routes mounted",
			slog.String("base", "/api/v1/auth/sessions"))
	} else {
		logger.Warn("auth/sessions: skipping mount; session manager is nil")
	}

	// Email verification flow (POST /api/v1/auth/verify/send, GET
	// /api/v1/auth/verify). The send endpoint is RequireSession; the
	// GET endpoint is anonymous (token possession IS the credential).
	if pool == nil || rdb == nil {
		logger.Warn("auth/verify: skipping mount; pool or redis is nil")
	} else if verifyUsers, err := authverify.NewPgxUserVerifier(pool); err != nil {
		logger.Warn("auth/verify: failed to build user verifier", slog.Any("err", err))
	} else if verifyTokens, err := authverify.NewRedisTokenStore(rdb); err != nil {
		logger.Warn("auth/verify: failed to build token store", slog.Any("err", err))
	} else {
		verifyBase := strings.TrimRight(cfg.Email.SiteURL, "/")
		if verifyBase == "" {
			verifyBase = "http://localhost"
		}
		verifyHandler, err := authverify.New(authverify.Options{
			Tokens:    verifyTokens,
			Users:     verifyUsers,
			Sender:    email.NewNoopSender(),
			VerifyURL: verifyBase + "/api/v1/auth/verify",
			Log:       logger,
		})
		if err != nil {
			logger.Warn("auth/verify: failed to build handler", slog.Any("err", err))
		} else {
			verifyHandler.Routes(mux, authmw.RequireSession(sessions))
			logger.Info("auth/verify: routes mounted",
				slog.String("send", "/api/v1/auth/verify/send"),
				slog.String("verify", "/api/v1/auth/verify"))
		}
	}

	// Posts REST surface (CRUD over /api/v1/posts). MemoryStore is
	// intentional here — the PgStore lands with the shared DAO follow-
	// up; the in-memory implementation keeps the K4 e2e able to drive
	// the admin end-to-end against a stubbed corpus.
	postsStore := restposts.NewMemoryStore()
	postsPolicy := policy.NewBasicPolicy(policy.DefaultRoleCapabilities())
	if err := restposts.Mount(mux, "/api/v1/posts", restposts.Deps{
		Store:    postsStore,
		Policy:   postsPolicy,
		Audit:    audit.NewEmitter(audit.NewMemoryStore()),
		Logger:   logger,
		PostType: restposts.PostTypePost,
	}); err != nil {
		logger.Warn("rest/posts: failed to mount", slog.Any("err", err))
	} else {
		logger.Info("rest/posts: routes mounted", slog.String("base", "/api/v1/posts"))
	}

	// Autosave routes (POST/GET /api/v1/posts/{id}/autosave). The
	// production store is Postgres-backed (migration 000016); the
	// MemoryAutosaveStore stays available for tests under
	// rest/posts/. The store is borrowed from `pool`; the cron sweep
	// registered below uses the same handle.
	//
	// When pool is nil (a misconfigured boot — the readyz check would
	// be unhappy too) we skip the mount rather than panic: a missing
	// autosave endpoint is the right failure mode, the renderer
	// surface keeps serving public content.
	var autosaveStore *restposts.PgxAutosaveStore
	if pool == nil {
		logger.Warn("rest/posts/autosave: skipping mount; pool is nil")
	} else {
		autosaveStore = restposts.NewPgxAutosaveStore(pool)
		if err := restposts.MountAutosave(mux, "/api/v1/posts", restposts.AutosaveDeps{
			PostStore:     postsStore,
			AutosaveStore: autosaveStore,
			Policy:        postsPolicy,
			PostType:      restposts.PostTypePost,
		}); err != nil {
			logger.Warn("rest/posts/autosave: failed to mount", slog.Any("err", err))
		} else {
			logger.Info("rest/posts/autosave: routes mounted",
				slog.String("base", "/api/v1/posts"),
			)
		}
	}

	// Daily TTL sweep for post_autosaves (migration 000016 spec'd a
	// 7-day TTL). The cron registry is wired here so a future worker
	// boot can pick it up; the matching taskspec.Default
	// registration sets up the worker-side dispatch handler that
	// invokes Sweep. See packages/go/jobs/cron for the scheduler
	// lifecycle.
	//
	// We use a binary-owned cron.Registry rather than a package
	// singleton because cron schedules are owned by the binary's
	// wiring (one registry per worker process) — same convention as
	// the rest of jobs/cron.
	//
	// Registration only happens when the autosave store wired up
	// (i.e. pool != nil); a nil store has nothing to sweep.
	if autosaveStore != nil {
		cronReg := cron.NewRegistry()
		if err := restposts.RegisterAutosaveSweep(
			autosaveStore,
			taskspec.Default(),
			cronReg,
			logger,
		); err != nil {
			logger.Warn("rest/posts/autosave: failed to register sweep cron",
				slog.Any("err", err))
		} else {
			logger.Info("rest/posts/autosave: sweep cron registered",
				slog.String("schedule", restposts.AutosaveSweepSchedule),
				slog.String("task", restposts.AutosaveSweepTaskName),
			)
		}
	}

	// Public search (GET /api/v1/search). Backed by the FTS Store
	// from packages/go/search (issue #119) against the existing
	// posts.search_vector index. The IP-keyed limiter throttles the
	// public surface — 5 req/s burst, 0.5 r/s steady — which matches
	// the doc-default for unauthenticated read paths.
	searchLimiter, slErr := ratelimit.NewMemoryLimiter(ratelimit.Policy{
		Capacity:   5,
		RefillRate: 0.5,
	})
	if pool == nil {
		logger.Warn("rest/search: skipping mount; pool is nil")
	} else if slErr != nil {
		logger.Warn("rest/search: failed to build limiter", slog.Any("err", slErr))
	} else {
		searchStore := pkgsearch.NewStore(pool)
		searchHandler := restsearch.NewHandler(searchStore, logger)
		if err := restsearch.Mount(mux, "/api/v1", searchLimiter, searchHandler); err != nil {
			logger.Warn("rest/search: failed to mount", slog.Any("err", err))
		} else {
			logger.Info("rest/search: routes mounted",
				slog.String("base", "/api/v1/search"))
		}
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
