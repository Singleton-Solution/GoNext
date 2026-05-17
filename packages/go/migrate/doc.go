// Package migrate wraps golang-migrate/migrate/v4 with the conventions
// the rest of GoNext expects: structured logging, typed config, and a
// Postgres advisory lock around every operation so multi-replica boots
// don't race on the schema_migrations table.
//
// The package is intentionally thin. It is not a query builder, not a
// migration generator, and not a runtime schema describer — those jobs
// live in `gonext migrate` (the CLI subcommand built on this package)
// and in the migration files under /migrations themselves.
//
// Typical usage:
//
//	if err := migrate.Run(ctx, cfg.Database, logger); err != nil {
//	    return fmt.Errorf("migrate: %w", err)
//	}
//
// The advisory lock key is a fixed 64-bit constant; any process holding
// it blocks any other process trying to run migrations against the same
// database. The lock is released on completion (success or failure) and
// is automatically released if the session dies.
//
// See ADR 0006 (monorepo layout), issue #96 (this runner), and
// migrations/README.md for the file-format contract.
package migrate
