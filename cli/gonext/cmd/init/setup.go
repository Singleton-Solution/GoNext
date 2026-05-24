package initcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	pkgmigrate "github.com/Singleton-Solution/GoNext/packages/go/migrate"
	"github.com/Singleton-Solution/GoNext/packages/go/theme/seed"
)

// installationCompletedKey is the options-table key written at the
// end of a successful init. Its presence is the gate for the
// idempotent re-run check: a row here, regardless of value, means
// "this install was bootstrapped".
//
// We don't use seed.ActiveThemeOptionKey for the gate because an
// operator might legitimately reset a theme to gn-hello after a
// failed migration and we don't want re-running init to be
// considered "already done" in that case. The two checks
// complement each other: active_theme detects pre-init databases
// that were bootstrapped by `migrate up --seed-default-theme` (and
// would otherwise have init double-seed the row).
//
// The value matches setup.InstallationOptionKey in
// apps/api/internal/setup so the in-browser install wizard and the
// CLI agree on a single canonical row. An earlier version of init
// wrote to "core.installation_completed_at" (no .site. segment);
// legacyInstallationCompletedKey below preserves that name so
// existing databases bootstrapped by the pre-fix CLI are detected
// and migrated forward.
const installationCompletedKey = "core.site.installation_completed_at"

// legacyInstallationCompletedKey is the pre-fix value of
// installationCompletedKey. We probe for it during the idempotency
// check and, if present, copy the row to the new canonical key and
// drop the old — a one-time, in-process migration that runs the
// next time `gonext init` is invoked against a previously
// bootstrapped install. The same intent is captured statically by
// migration 000028_options_installation_key_compat for databases
// that never re-run init.
const legacyInstallationCompletedKey = "core.installation_completed_at"

// siteNameKey and siteURLKey are the canonical options keys for the
// human-facing site metadata captured by `gonext init`. These match
// the keys the public renderer reads at request time (see
// config.PublicSite + the admin Site settings panel) so a single
// source of truth lives in the options table.
const (
	siteNameKey = "core.site.name"
	siteURLKey  = "core.site.url"
)

// SetupOptions is the typed payload setup.Run consumes. Built by
// init.go from flags + prompts, but the orchestrator does NOT depend
// on flag.FlagSet or os.Stdin — tests construct SetupOptions directly
// and drive Run.
type SetupOptions struct {
	// DSN is the Postgres connection string. Required.
	DSN string

	// MigrationDir is the directory `migrate up` consults. Empty
	// falls back to ./migrations, matching the rest of the CLI.
	MigrationDir string

	// ThemeDir is the runtime theme directory the seeder unpacks
	// gn-hello into. Empty falls back to ./themes.
	ThemeDir string

	// Pepper is the GONEXT_AUTH_PEPPER value. Required — the password
	// hasher won't produce a verifiable hash without it (formally it
	// would, but the resulting hash wouldn't match what the API
	// process produces at login time).
	Pepper []byte

	// AdminEmail / AdminPassword are the captured admin credentials.
	// Validated by createAdmin.
	AdminEmail    string
	AdminPassword string

	// SiteName / SiteURL are the optional site metadata. Empty values
	// are not written — the renderer falls back to its defaults.
	SiteName string
	SiteURL  string

	// SkipMigrations / SkipThemeSeed bypass those steps. Used when an
	// operator has already applied them via a separate channel
	// (kube initContainer, custom theme rolled out before init).
	SkipMigrations bool
	SkipThemeSeed  bool

	// Logger receives structured progress lines. Nil falls back to
	// slog.Default() — the orchestrator never panics on a missing
	// logger.
	Logger *slog.Logger
}

// stepFailure is the error type returned by Run when a specific
// step in the orchestrator fails. It carries the name of the failing
// step so the CLI can render "init failed at <step>: ..." without
// having to string-match on the underlying error.
type stepFailure struct {
	step string
	err  error
}

func (s *stepFailure) Error() string {
	return fmt.Sprintf("%s: %v", s.step, s.err)
}

func (s *stepFailure) Unwrap() error { return s.err }

// failedStep returns the step name of err if it is a stepFailure, or
// "" otherwise. Used by tests to assert which step blew up without
// brittle string matching.
func failedStep(err error) string {
	var sf *stepFailure
	if errors.As(err, &sf) {
		return sf.step
	}
	return ""
}

// Setup is the orchestrator. It runs the steps in fixed order:
//
//	1. ensure connection — opens a pool and Pings. Fails fast with a
//	   clear "connect: ..." error instead of letting later steps
//	   surface confusing follow-on failures.
//	2. idempotency probe — if installation_completed_at exists, the
//	   whole flow is a no-op and Setup returns (true, nil). We probe
//	   via a short-lived pool that's reused below.
//	3. migrations — equivalent to `gonext migrate up`. Skipped under
//	   --skip-migrations.
//	4. theme seed — equivalent to the seed step `migrate up` runs.
//	   Skipped under --skip-theme-seed.
//	5. admin — createAdmin in a transaction.
//	6. site options — writes core.site.name / core.site.url when set.
//	7. completion — writes core.installation_completed_at = now().
//
// Each step's error is wrapped in a stepFailure so tests and the CLI
// can identify which phase blew up. The pool is owned by Setup and
// closed before return.
func Setup(ctx context.Context, opts SetupOptions) (alreadyDone bool, err error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if opts.DSN == "" {
		return false, &stepFailure{step: "config", err: errors.New("DATABASE_URL is required")}
	}
	if len(opts.Pepper) == 0 {
		return false, &stepFailure{step: "config", err: errors.New("GONEXT_AUTH_PEPPER is required")}
	}

	// Step 1: connect. We open a pgxpool with a short Ping budget so
	// a wrong DSN fails in seconds, not minutes.
	dbCfg := config.DatabaseConfig{
		URL:              opts.DSN,
		MigrationDir:     opts.MigrationDir,
		StatementTimeout: 0, // init runs interactively; no per-statement cap.
	}
	if dbCfg.MigrationDir == "" {
		dbCfg.MigrationDir = "./migrations"
	}

	pool, err := openPool(ctx, opts.DSN)
	if err != nil {
		return false, &stepFailure{step: "connect", err: err}
	}
	defer pool.Close()

	// Step 2: idempotency. Two layered checks:
	//
	//   a. installation_completed_at exists       => "already done"
	//   b. active_theme exists                    => "treat as done", because
	//      an older bootstrap path produced a usable install without
	//      writing installation_completed_at. We back-fill the row so
	//      future re-runs short-circuit on (a).
	//
	// If neither row is present we proceed.
	done, viaTheme, err := alreadyInstalled(ctx, pool)
	if err != nil {
		return false, &stepFailure{step: "probe", err: err}
	}
	if done {
		logger.Info("gonext init: already completed; nothing to do")
		if viaTheme {
			// Back-fill the explicit gate so subsequent re-runs are
			// detected by (a) and don't have to re-probe the theme row.
			if wErr := writeCompletedAt(ctx, pool); wErr != nil {
				// Best-effort: a failure here is not fatal because the
				// install IS already usable. Log and continue.
				logger.Warn("gonext init: back-fill installation_completed_at failed",
					slog.Any("err", wErr))
			}
		}
		return true, nil
	}

	// Step 3: migrations.
	if !opts.SkipMigrations {
		if err := pkgmigrate.Run(ctx, dbCfg, logger); err != nil {
			return false, &stepFailure{step: "migrate", err: err}
		}
		logger.Info("gonext init: migrations applied")
	} else {
		logger.Info("gonext init: migrations skipped (--skip-migrations)")
	}

	// Step 4: theme seed.
	if !opts.SkipThemeSeed {
		if err := runThemeSeed(ctx, pool, opts.ThemeDir, logger); err != nil {
			return false, &stepFailure{step: "theme-seed", err: err}
		}
		logger.Info("gonext init: default theme seeded")
	} else {
		logger.Info("gonext init: theme seed skipped (--skip-theme-seed)")
	}

	// Step 5: admin. Wrapped in a transaction so the users +
	// user_passwords rows land atomically. A partial write here would
	// leave the install with an unfixable login.
	if err := withTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := createAdmin(ctx, tx, adminInput{
			email:    opts.AdminEmail,
			password: opts.AdminPassword,
			pepper:   opts.Pepper,
		})
		return err
	}); err != nil {
		return false, &stepFailure{step: "admin", err: err}
	}
	logger.Info("gonext init: super_admin created", slog.String("email", opts.AdminEmail))

	// Step 6: site options. These are independent rows; we use the
	// shared writeOptionRow helper which UPSERTs with the same flag
	// shape as the rest of the options table (autoload=TRUE,
	// is_protected=FALSE) so the admin can edit them later.
	if opts.SiteName != "" {
		if err := writeOptionRow(ctx, pool, siteNameKey, opts.SiteName); err != nil {
			return false, &stepFailure{step: "site-options", err: fmt.Errorf("write site name: %w", err)}
		}
	}
	if opts.SiteURL != "" {
		if err := writeOptionRow(ctx, pool, siteURLKey, opts.SiteURL); err != nil {
			return false, &stepFailure{step: "site-options", err: fmt.Errorf("write site url: %w", err)}
		}
	}

	// Step 7: completion marker. The value is the ISO 8601 timestamp;
	// the CLI doesn't read it back but ops dashboards can use it to
	// answer "when was this install bootstrapped?".
	if err := writeCompletedAt(ctx, pool); err != nil {
		return false, &stepFailure{step: "complete", err: err}
	}
	logger.Info("gonext init: complete")
	return false, nil
}

// openPool dials Postgres with a short Ping timeout so a bad DSN
// fails fast. We don't go through packages/go/db.New here because
// that path requires a statement_timeout and various other knobs we
// don't want bound to a one-shot bootstrap process.
func openPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(dialCtx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// alreadyInstalled returns (done, viaTheme, err). done is true if
// either (a) installation_completed_at is set, or (b) active_theme
// is set. viaTheme reports the (b) path so the caller can decide
// whether to back-fill the explicit completion marker.
//
// The two queries are independent because the options table may
// have been bootstrapped via `migrate up --seed-default-theme=true`
// without ever running this command — the active_theme row exists
// but installation_completed_at does not. We treat that combination
// as "already installed" and patch the explicit gate.
//
// A pre-fix CLI wrote the marker under legacyInstallationCompletedKey.
// We detect that row here and migrate it forward to the canonical
// key so the setup handler (which reads only the canonical key)
// also sees the install as complete on the very next request.
func alreadyInstalled(ctx context.Context, pool *pgxpool.Pool) (bool, bool, error) {
	// Probe the explicit gate first. A row here is the authoritative
	// "this install was bootstrapped by gonext init" signal.
	//
	// The options table may not exist yet on a brand-new database
	// that has never had migrations applied. We treat the missing-
	// table error as "not installed" so the migrate step can run
	// and create it; any other SQL failure is surfaced.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM options WHERE key = $1)`,
		installationCompletedKey,
	).Scan(&exists); err != nil {
		if isUndefinedTable(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("probe %s: %w", installationCompletedKey, err)
	}
	if exists {
		return true, false, nil
	}

	// Legacy-key probe. If a previous-version CLI wrote the marker
	// under "core.installation_completed_at", carry it forward to the
	// canonical key and drop the old row. The setup handler reads only
	// the canonical key, so without this migration `/api/v1/setup/status`
	// would falsely report installation_completed=false on an already
	// bootstrapped install.
	if migrated, err := migrateLegacyInstallationKey(ctx, pool); err != nil {
		return false, false, fmt.Errorf("migrate legacy %s: %w", legacyInstallationCompletedKey, err)
	} else if migrated {
		return true, false, nil
	}

	// Fallback probe: active_theme. If present, an earlier `migrate
	// up` already produced a usable install — running init again
	// would clobber the admin and re-seed the theme.
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM options WHERE key = $1)`,
		seed.ActiveThemeOptionKey,
	).Scan(&exists); err != nil {
		// Same "fresh DB" safeguard as above.
		if isUndefinedTable(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("probe %s: %w", seed.ActiveThemeOptionKey, err)
	}
	return exists, exists, nil
}

// migrateLegacyInstallationKey copies the legacy
// "core.installation_completed_at" row (if any) to the canonical
// "core.site.installation_completed_at" key, then deletes the
// legacy row. Idempotent: running it twice is a no-op once the
// legacy row is gone. Returns (true, nil) iff the migration ran.
//
// The function exits early on the "fresh DB / no options table"
// path so a brand-new install isn't penalized by an extra round
// trip. The work is best-effort transactional — both statements
// run in a single tx so a failure mid-migration can't leave the
// canonical key written without the legacy key dropped.
func migrateLegacyInstallationKey(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var legacyValue string
	err := pool.QueryRow(ctx,
		`SELECT value::text FROM options WHERE key = $1`,
		legacyInstallationCompletedKey,
	).Scan(&legacyValue)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if isUndefinedTable(err) {
			return false, nil
		}
		return false, fmt.Errorf("read legacy row: %w", err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO options (key, value, autoload, is_protected)
		VALUES ($1, $2::jsonb, TRUE, FALSE)
		ON CONFLICT (key) DO NOTHING
	`, installationCompletedKey, legacyValue); err != nil {
		return false, fmt.Errorf("copy to canonical key: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM options WHERE key = $1`,
		legacyInstallationCompletedKey,
	); err != nil {
		return false, fmt.Errorf("delete legacy row: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

// isUndefinedTable returns true iff err is a Postgres
// "undefined_table" (SQLSTATE 42P01). We test this with a structural
// SQLState() check so the helper works against any pgconn error type
// without dragging the pgconn package into the import surface.
func isUndefinedTable(err error) bool {
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		return sqlState.SQLState() == "42P01"
	}
	return false
}

// runThemeSeed runs the bundled-theme seeder against the supplied
// pool. We rebuild the seeder per call because it owns no
// long-lived state and the cost is negligible.
func runThemeSeed(ctx context.Context, pool *pgxpool.Pool, themeDir string, logger *slog.Logger) error {
	if themeDir == "" {
		themeDir = "./themes"
	}
	s := &seed.Seeder{
		DB:       seed.PoolQuerier{Pool: pool},
		ThemeDir: themeDir,
		SourceFS: seed.BundledThemes,
		Logger:   logger,
	}
	return s.EnsureDefault(ctx)
}

// withTx wraps fn in a pgx.Tx. We use the default isolation level —
// the admin INSERT is two rows in unrelated tables; we don't need
// SERIALIZABLE here, only atomicity.
func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is a no-op when called after a successful Commit, so
	// the defer is always safe.
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// writeOptionRow UPSERTs a single options-table row. We use UPSERT
// rather than ON CONFLICT DO NOTHING because the operator is the
// authority here — if they re-ran init with a new --site-name, they
// expect the new value to land. The idempotency gate at the
// orchestrator level (alreadyInstalled) prevents the more dangerous
// "re-run clobbers a customized name" scenario.
//
// autoload=TRUE because the site name/URL are read on virtually every
// admin render; we want them in the boot-time hot cache.
func writeOptionRow(ctx context.Context, pool *pgxpool.Pool, key, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO options (key, value, autoload, is_protected)
		VALUES ($1, $2::jsonb, TRUE, FALSE)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, key, string(encoded)); err != nil {
		return fmt.Errorf("upsert %s: %w", key, err)
	}
	return nil
}

// writeCompletedAt is the final write: the gate row that future
// re-runs check. The value is now() formatted as an RFC 3339 string;
// the format isn't important to the gate (the existence check is),
// but it makes ops dashboards readable.
func writeCompletedAt(ctx context.Context, pool *pgxpool.Pool) error {
	return writeOptionRow(ctx, pool, installationCompletedKey, time.Now().UTC().Format(time.RFC3339))
}

// readPepperFromEnv pulls GONEXT_AUTH_PEPPER. We re-implement the
// lookup here (rather than going through packages/go/config) because
// config.Load() requires SessionSecret + CSRFSecret + a dozen other
// fields that init's caller hasn't set up yet. The pepper is the
// only auth secret init touches.
func readPepperFromEnv() []byte {
	if v := os.Getenv("GONEXT_AUTH_PEPPER"); v != "" {
		return []byte(v)
	}
	return nil
}
