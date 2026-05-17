// Package db is the Postgres connection pool for GoNext binaries that
// talk to the primary database (apps/api, apps/worker, cli/gonext).
//
// It wraps pgx/v5's pgxpool with three opinionated additions:
//
//   - Config-driven setup via packages/go/config — DSN, pool sizes,
//     lifetimes, and the per-connection statement_timeout all come from
//     the typed Config struct.
//
//   - Per-connection statement_timeout, applied via an AfterConnect hook.
//     This is essential — a runaway query in one handler can wedge a
//     pool slot for minutes otherwise.
//
//   - Boot-time Ping verification with a clear error if the DB is
//     unreachable. Failing fast at startup is preferable to failing
//     the first user request.
//
// The pool is process-wide: pass it by pointer to handlers, services,
// and the worker via a dependency container. Do not re-acquire on
// each request.
//
// Typical wiring in cmd/server/main.go:
//
//	pool, err := db.New(ctx, cfg.Database, logger)
//	if err != nil {
//	    return fmt.Errorf("db: %w", err)
//	}
//	defer pool.Close()
//
// See docs/01-core-cms.md §11 (pgx chosen over sqlc/ent), ADR 0004
// (Postgres as primary store), and the operational notes in
// docs/09-deployment-ops.md §17 (resource sizing).
package db
