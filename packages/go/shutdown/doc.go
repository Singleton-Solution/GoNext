// Package shutdown is the cross-component drain orchestrator for GoNext
// binaries that own more than just an HTTP listener.
//
// The httpx chassis (packages/go/httpx) ships a graceful shutdown for
// the http.Server, but the API binary also owns a pgx pool, a Redis
// client, a metrics exporter, and an audit emitter; the worker binary
// adds an Asynq queue consumer and a cron leader-election lease. Each
// of those resources has its own Close/Stop method with its own timing
// constraints. Calling them ad hoc in main.go (with a stack of `defer`s)
// has three problems:
//
//  1. The order is wrong. `defer` is LIFO over the registration order,
//     which is the right order — but `defer` runs after Run returns, so
//     the HTTP listener is already closed by the time Close is called.
//     The HTTP server should be drained while the DB pool is still up
//     (in-flight requests may need to write their last response).
//
//  2. The budget is unbounded. K8s gives the pod 30 seconds between
//     SIGTERM and SIGKILL by default; if any Close hangs (a Redis
//     PubSub client waiting on a server roundtrip, say), the pod dies
//     mid-drain and in-flight requests get dropped.
//
//  3. Errors get swallowed. The `_ = rdb.Close()` pattern is universal
//     but means we never know why a shutdown stalled.
//
// Orchestrator solves all three:
//
//   - Register the resources in dependency order: stop accepting work
//     first (HTTP, queue consumer), then in-flight workers (audit
//     emitter, metrics flusher), then connection pools (Redis, DB) last.
//
//   - Drain runs in REVERSE registration order (LIFO). That gives us:
//     stop accepting work → drain in-flight → close persistence. The
//     ordering matches `defer` semantics, which is the mental model Go
//     programmers already have.
//
//   - The total budget is split proportionally across the registered
//     closers. Each closer gets its own derived context that fires when
//     its slice expires, so one slow Close cannot eat the whole budget.
//
//   - Every closer is called, even if an earlier one errored. We're in
//     the shutdown path; the goal is to drain as much as possible, not
//     to bail on the first failure. The first error is returned (joined
//     with later errors) so the caller can log a single root cause.
//
//   - Each step is logged at INFO with its duration so operators can see
//     "redis.close took 4.2s" in the same log stream that triggered the
//     drain.
//
// Typical wiring in cmd/server/main.go:
//
//	orch := shutdown.New(shutdown.Options{
//	    Log:    logger,
//	    Budget: cfg.Server.ShutdownTimeout, // shared default 30s
//	})
//
//	// Register in dependency order: HTTP first (stops accepting), DB last
//	// (everything else may need it during drain).
//	orch.Register("http.server", srv.Shutdown)
//	orch.Register("audit.emitter", emitter.Close)
//	orch.Register("metrics.flusher", metrics.Close)
//	orch.Register("redis.client", func(ctx context.Context) error { return rdb.Close() })
//	orch.Register("db.pool", func(ctx context.Context) error { pool.Close(); return nil })
//
//	// Block until a signal arrives, then drain.
//	if err := orch.Wait(ctx); err != nil {
//	    return fmt.Errorf("shutdown: %w", err)
//	}
//
// See docs/09-deployment-ops.md §11 (Graceful shutdown contract) and
// issue #112 for the design discussion.
package shutdown
