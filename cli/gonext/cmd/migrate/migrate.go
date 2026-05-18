// Package migrate. See doc.go.
package migrate

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	pkgmigrate "github.com/Singleton-Solution/GoNext/packages/go/migrate"
	"github.com/Singleton-Solution/GoNext/packages/go/theme/seed"
)

// Exit codes shared with main.go and tests.
const (
	ExitOK    = 0
	ExitFail  = 1
	ExitUsage = 2
)

const usage = `gonext migrate — run database migrations

Usage:
  gonext migrate <subcommand> [args]

Subcommands:
  up [flags]       Apply every pending up migration. Idempotent.
                   Flags:
                     --seed-default-theme=BOOL  Install the bundled default
                                                theme (gn-hello) on first
                                                boot. Default: true.
  down [N]         Roll back N migrations (default 1). Pass 0 to roll back ALL.
  status           Print the current schema version and dirty flag.
  wp               Import a WordPress WXR export. See 'migrate wp --help'.
  verify           Verify a WordPress import for fidelity. See 'migrate verify --help'.

Environment:
  DATABASE_URL              Required. Postgres DSN.
  GONEXT_MIGRATION_DIR      Migration directory. Default: ./migrations.
  GONEXT_THEME_DIR          Runtime theme directory used by the seeder.
                            Default: ./themes.

Exit codes:
  0   success
  1   migration error
  2   usage error`

// Run dispatches `gonext migrate ...`. args is the slice after the
// literal `migrate` token. Returns the desired process exit code.
//
// stdout/stderr are injected so tests can capture output.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return ExitOK
	case "up":
		return runUp(args[1:], stdout, stderr)
	case "down":
		return runDown(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "wp":
		return runWP(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "gonext migrate: unknown subcommand %q\n\n%s\n", args[0], usage)
		return ExitUsage
	}
}

// RunOS is a convenience that wires Run to os.Stdout/os.Stderr.
func RunOS(args []string) int { return Run(args, os.Stdout, os.Stderr) }

// runUp applies all pending up migrations and, if --seed-default-theme
// is on (the default), runs the theme seeder so a fresh deploy renders
// a usable site.
//
// We put the seed step here — directly after the migration runner — so
// the boot-time invariant "after this command, the DB is at the latest
// schema AND a theme is active" holds for every operator path: kube
// initContainers, `make up`, Compose, bare `gonext migrate up` on a
// dev box. The seeder is idempotent, so re-running migrate up on an
// already-seeded install is a no-op.
func runUp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate up", flag.ContinueOnError)
	fs.SetOutput(stderr)
	seedDefaultTheme := fs.Bool("seed-default-theme", true,
		"install the bundled default theme (gn-hello) on first boot")
	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already prints the error; emit usage so
		// the operator sees the surrounding command help too.
		fmt.Fprintln(stderr, usage)
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "gonext migrate up: unexpected argument %q\n\n%s\n", fs.Arg(0), usage)
		return ExitUsage
	}
	cfg, logger, code := loadConfig(stderr)
	if code != ExitOK {
		return code
	}
	ctx, cancel := contextWithCancel()
	defer cancel()
	if err := pkgmigrate.Run(ctx, cfg, logger); err != nil {
		fmt.Fprintf(stderr, "gonext migrate up: %v\n", err)
		return ExitFail
	}
	fmt.Fprintln(stdout, "migrate: up OK")

	if !*seedDefaultTheme {
		logger.Info("theme seed skipped via --seed-default-theme=false")
		return ExitOK
	}
	if err := runThemeSeed(ctx, cfg, logger); err != nil {
		fmt.Fprintf(stderr, "gonext migrate up: theme seed: %v\n", err)
		return ExitFail
	}
	fmt.Fprintln(stdout, "migrate: theme seed OK")
	return ExitOK
}

// runThemeSeed opens a transient pgxpool, constructs a Seeder, and
// runs EnsureDefault. The pool is closed before return — the seed
// step is a one-shot phase, not a long-lived resource.
//
// Errors are returned wrapped; the caller emits the user-facing
// "gonext migrate up: theme seed: ..." prefix.
func runThemeSeed(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) (retErr error) {
	pool, err := pgxpool.New(ctx, cfg.URL)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	themeDir := os.Getenv("GONEXT_THEME_DIR")
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

// runDown rolls back N migrations (or all if 0). Default N is 1.
func runDown(args []string, stdout, stderr io.Writer) int {
	steps := 1
	if len(args) > 1 {
		fmt.Fprintf(stderr, "gonext migrate down: too many arguments\n\n%s\n", usage)
		return ExitUsage
	}
	if len(args) == 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 {
			fmt.Fprintf(stderr, "gonext migrate down: invalid steps %q (want a non-negative integer)\n", args[0])
			return ExitUsage
		}
		steps = n
	}
	cfg, logger, code := loadConfig(stderr)
	if code != ExitOK {
		return code
	}
	ctx, cancel := contextWithCancel()
	defer cancel()
	if err := pkgmigrate.Down(ctx, cfg, logger, steps); err != nil {
		fmt.Fprintf(stderr, "gonext migrate down: %v\n", err)
		return ExitFail
	}
	fmt.Fprintf(stdout, "migrate: down %d OK\n", steps)
	return ExitOK
}

// runStatus prints the current version + dirty flag.
func runStatus(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "gonext migrate status: unexpected argument %q\n\n%s\n", args[0], usage)
		return ExitUsage
	}
	cfg, logger, code := loadConfig(stderr)
	if code != ExitOK {
		return code
	}
	ctx, cancel := contextWithCancel()
	defer cancel()
	cur, dirty, err := pkgmigrate.Status(ctx, cfg, logger)
	if err != nil {
		fmt.Fprintf(stderr, "gonext migrate status: %v\n", err)
		return ExitFail
	}
	fmt.Fprintf(stdout, "version: %d\ndirty: %t\n", cur, dirty)
	return ExitOK
}

// loadConfig pulls the database DSN and migration directory from the
// environment. We don't go through the full config.Load() machinery
// because that one requires unrelated secrets (CSRF/pepper/session)
// that an operator running `gonext migrate up` from a one-off box
// shouldn't have to set. Migrations only need DATABASE_URL and
// (optionally) GONEXT_MIGRATION_DIR.
func loadConfig(stderr io.Writer) (config.DatabaseConfig, *slog.Logger, int) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(stderr, "gonext migrate: DATABASE_URL is required")
		return config.DatabaseConfig{}, nil, ExitUsage
	}
	dir := os.Getenv("GONEXT_MIGRATION_DIR")
	if dir == "" {
		dir = "./migrations"
	}
	cfg := config.DatabaseConfig{URL: url, MigrationDir: dir}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return cfg, logger, ExitOK
}

// contextWithCancel returns a context with a generous overall budget
// for the migration. Individual operations inside the package set
// their own tighter timeouts (e.g. the advisory-lock acquisition).
func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}
