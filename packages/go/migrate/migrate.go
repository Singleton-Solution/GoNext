// Package migrate. See doc.go.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// advisoryLockKey is the fixed 64-bit constant used as the Postgres
// session-level advisory lock for every migration operation. The value
// is arbitrary but stable; if it ever changes, in-flight deploys
// straddling the change could deadlock. The high bits ("GONEXTMG") are
// just a mnemonic — it's a single int64.
const advisoryLockKey int64 = 0x474f4e455854_4d47 //nolint:revive // mnemonic constant

// lockTimeout caps how long any single advisory lock acquisition will
// wait before we give up. In practice migrations are short, and a long
// wait here is a signal that another replica is doing the work — we
// surface that as an error rather than blocking the boot indefinitely.
const lockTimeout = 2 * time.Minute

// Run applies every pending up migration in cfg.MigrationDir against
// cfg.URL. It is idempotent: invoking Run after Run does nothing.
//
// A Postgres session-level advisory lock is taken around the operation
// so that concurrent boots from multiple replicas serialise without
// stomping on schema_migrations. The lock is released on return.
//
// logger is required for structured progress lines. Nil is tolerated —
// falls back to slog.Default.
func Run(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) error {
	logger = ensureLogger(logger)
	return withLock(ctx, cfg, logger, "up", func(m *migrate.Migrate) error {
		start := time.Now()
		err := m.Up()
		switch {
		case err == nil:
			ver, dirty := currentVersion(m)
			logger.Info("migrations applied",
				slog.String("op", "up"),
				slog.Uint64("version", uint64(ver)),
				slog.Bool("dirty", dirty),
				slog.Duration("took", time.Since(start)),
			)
			return nil
		case errors.Is(err, migrate.ErrNoChange):
			ver, dirty := currentVersion(m)
			logger.Info("migrations up to date",
				slog.String("op", "up"),
				slog.Uint64("version", uint64(ver)),
				slog.Bool("dirty", dirty),
			)
			return nil
		default:
			return fmt.Errorf("migrate.Run: up: %w", err)
		}
	})
}

// Down rolls back migrations.
//
// If steps > 0, exactly that many migrations are reverted (or fewer if
// fewer have been applied). If steps == 0, every migration is reverted
// — typically only useful in dev/test.
//
// Like Run, it acquires a Postgres advisory lock for the duration.
func Down(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger, steps int) error {
	logger = ensureLogger(logger)
	if steps < 0 {
		return fmt.Errorf("migrate.Down: steps must be >= 0, got %d", steps)
	}
	return withLock(ctx, cfg, logger, "down", func(m *migrate.Migrate) error {
		start := time.Now()
		var err error
		if steps == 0 {
			logger.Warn("rolling back ALL migrations",
				slog.String("op", "down"),
				slog.Int("steps", 0),
			)
			err = m.Down()
		} else {
			err = m.Steps(-steps)
		}
		switch {
		case err == nil:
			ver, dirty := currentVersion(m)
			logger.Info("migrations rolled back",
				slog.String("op", "down"),
				slog.Int("steps", steps),
				slog.Uint64("version", uint64(ver)),
				slog.Bool("dirty", dirty),
				slog.Duration("took", time.Since(start)),
			)
			return nil
		case errors.Is(err, migrate.ErrNoChange):
			logger.Info("no migrations to roll back",
				slog.String("op", "down"),
				slog.Int("steps", steps),
			)
			return nil
		default:
			return fmt.Errorf("migrate.Down: %w", err)
		}
	})
}

// To migrates the schema to a specific target version, going up or
// down as necessary. If the current version is below target, pending
// up migrations are applied until version == target; if above, down
// migrations are rolled back until version == target. If the current
// version already equals target the call is a no-op.
//
// target must be a positive migration version (matching the
// `NNNNNN_*.{up,down}.sql` filename prefix). Passing 0 returns an
// error — to roll back ALL migrations, use Down(ctx, cfg, logger, 0)
// explicitly so the destructive intent is visible at the call site.
//
// Like Run/Down, To acquires a Postgres advisory lock for the duration.
func To(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger, target uint) error {
	logger = ensureLogger(logger)
	if target == 0 {
		return fmt.Errorf("migrate.To: target must be > 0 (use Down(..., 0) to roll back all)")
	}
	return withLock(ctx, cfg, logger, "to", func(m *migrate.Migrate) error {
		start := time.Now()
		err := m.Migrate(target)
		switch {
		case err == nil:
			ver, dirty := currentVersion(m)
			logger.Info("migrated to target version",
				slog.String("op", "to"),
				slog.Uint64("target", uint64(target)),
				slog.Uint64("version", uint64(ver)),
				slog.Bool("dirty", dirty),
				slog.Duration("took", time.Since(start)),
			)
			return nil
		case errors.Is(err, migrate.ErrNoChange):
			ver, dirty := currentVersion(m)
			logger.Info("already at target version",
				slog.String("op", "to"),
				slog.Uint64("target", uint64(target)),
				slog.Uint64("version", uint64(ver)),
				slog.Bool("dirty", dirty),
			)
			return nil
		default:
			return fmt.Errorf("migrate.To: %w", err)
		}
	})
}

// Status reports the current schema_migrations state.
//
// current is the highest applied migration version (0 if none).
// dirty is true if a prior migration left the schema in an
// inconsistent state — operators must inspect and force-resolve.
func Status(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) (current uint, dirty bool, err error) {
	logger = ensureLogger(logger)
	err = withLock(ctx, cfg, logger, "status", func(m *migrate.Migrate) error {
		v, d, vErr := m.Version()
		if vErr != nil && !errors.Is(vErr, migrate.ErrNilVersion) {
			return fmt.Errorf("migrate.Status: version: %w", vErr)
		}
		if errors.Is(vErr, migrate.ErrNilVersion) {
			// No migrations have run yet. That's a valid status, not an error.
			current = 0
			dirty = false
		} else {
			current = v
			dirty = d
		}
		logger.Info("migration status",
			slog.String("op", "status"),
			slog.Uint64("version", uint64(current)),
			slog.Bool("dirty", dirty),
		)
		return nil
	})
	return current, dirty, err
}

// withLock opens the database, applies the advisory lock, builds the
// migrate.Migrate instance, runs fn, and tears everything down. The
// lock is automatically released when the session ends — we also
// release it explicitly on the happy path so the lock isn't held until
// the connection's idle timer fires.
func withLock(
	ctx context.Context,
	cfg config.DatabaseConfig,
	logger *slog.Logger,
	op string,
	fn func(*migrate.Migrate) error,
) (retErr error) {
	if cfg.URL == "" {
		return fmt.Errorf("migrate: DATABASE_URL is required (got empty)")
	}
	if cfg.MigrationDir == "" {
		return fmt.Errorf("migrate: MigrationDir is required (got empty)")
	}

	logger = logger.With(slog.String("op", op))

	// pgx's stdlib driver registers under "pgx". We use database/sql here
	// because golang-migrate's postgres driver expects a *sql.DB. The
	// connection is short-lived and never leaves this function.
	db, err := sql.Open("pgx", cfg.URL)
	if err != nil {
		return fmt.Errorf("migrate: open db: %w", err)
	}
	defer func() {
		if cErr := db.Close(); cErr != nil && retErr == nil {
			retErr = fmt.Errorf("migrate: close db: %w", cErr)
		}
	}()

	// One connection for the lock; another could be used by migrate. We
	// hold the lock on a dedicated connection so the entire migration
	// surface is serialised against other replicas.
	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	conn, err := db.Conn(lockCtx)
	if err != nil {
		return fmt.Errorf("migrate: acquire conn for lock: %w", err)
	}
	defer func() {
		if cErr := conn.Close(); cErr != nil && retErr == nil {
			retErr = fmt.Errorf("migrate: release lock conn: %w", cErr)
		}
	}()

	logger.Debug("acquiring advisory lock", slog.Int64("key", advisoryLockKey))
	if _, err := conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("migrate: pg_advisory_lock: %w", err)
	}
	defer func() {
		// Best-effort unlock. If this fails the session-close above will
		// drop the lock anyway, so we only log.
		if _, uErr := conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey); uErr != nil {
			logger.Warn("pg_advisory_unlock failed (lock will release on session close)", slog.String("err", uErr.Error()))
		}
	}()
	logger.Debug("advisory lock held")

	driver, err := migratepg.WithInstance(db, &migratepg.Config{})
	if err != nil {
		return fmt.Errorf("migrate: postgres driver: %w", err)
	}

	sourceURL, err := sourceURLForDir(cfg.MigrationDir)
	if err != nil {
		return fmt.Errorf("migrate: source url: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(sourceURL, "postgres", driver)
	if err != nil {
		return fmt.Errorf("migrate: new instance: %w", err)
	}
	defer func() {
		// migrate.Close returns (sourceErr, dbErr); the dbErr is a no-op
		// since we own the *sql.DB and close it ourselves above.
		if srcErr, _ := m.Close(); srcErr != nil && retErr == nil {
			retErr = fmt.Errorf("migrate: close source: %w", srcErr)
		}
	}()

	// Wire the logger into migrate's verbose path so source file
	// progress lines flow through slog at debug level.
	m.Log = slogMigrateLogger{logger: logger}

	return fn(m)
}

// sourceURLForDir turns a filesystem path into the file:// URL that
// golang-migrate's file source expects. The path is made absolute so
// migrations resolve correctly regardless of the caller's cwd.
func sourceURLForDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("absolute path for %q: %w", dir, err)
	}
	// On Windows filepath.Abs returns backslashes; url.URL.Path wants
	// forward slashes. ToSlash is a no-op on POSIX.
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	// On POSIX absolute paths begin with "/"; on Windows they begin
	// with "C:/" etc. golang-migrate's file source accepts both via
	// the file:// scheme.
	s := u.String()
	// strings.HasPrefix here is just a safety net — `url.URL{}.String()`
	// already produces "file:///abs/path".
	if !strings.HasPrefix(s, "file://") {
		return "", fmt.Errorf("unexpected source url: %q", s)
	}
	return s, nil
}

// currentVersion fetches the current version + dirty flag, swallowing
// the ErrNilVersion sentinel (which means "no migrations applied").
func currentVersion(m *migrate.Migrate) (uint, bool) {
	v, dirty, err := m.Version()
	if err != nil {
		return 0, false
	}
	return v, dirty
}

func ensureLogger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}

// slogMigrateLogger adapts a *slog.Logger to the migrate.Logger
// interface (Printf-style + Verbose()). Output goes to Debug because
// migrate's own log lines are file-by-file progress — useful for
// debugging, noisy by default.
type slogMigrateLogger struct {
	logger *slog.Logger
}

func (s slogMigrateLogger) Printf(format string, v ...interface{}) {
	s.logger.Debug(strings.TrimRight(fmt.Sprintf(format, v...), "\n"))
}

func (s slogMigrateLogger) Verbose() bool { return true }
