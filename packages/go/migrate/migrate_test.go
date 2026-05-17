package migrate

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// quietLogger returns a slog logger that discards all output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// dsnFromEnv returns a usable DATABASE_URL for integration tests, or
// SKIPs the test if unset. Matches the convention in packages/go/db.
func dsnFromEnv(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return dsn
}

// repoMigrationsDir locates the /migrations directory at the repo
// root. This file lives at packages/go/migrate/migrate_test.go, so
// "../../../migrations" resolves correctly.
func repoMigrationsDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Join(wd, "..", "..", "..", "migrations")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("migrations dir not found at %s: %v", dir, err)
	}
	return dir
}

func TestRun_RequiresURL(t *testing.T) {
	err := Run(context.Background(), config.DatabaseConfig{URL: "", MigrationDir: "./migrations"}, quietLogger())
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL, got: %v", err)
	}
}

func TestRun_RequiresMigrationDir(t *testing.T) {
	err := Run(context.Background(), config.DatabaseConfig{
		URL:          "postgres://x@x/x",
		MigrationDir: "",
	}, quietLogger())
	if err == nil {
		t.Fatal("expected error for empty MigrationDir")
	}
	if !strings.Contains(err.Error(), "MigrationDir") {
		t.Errorf("error should mention MigrationDir, got: %v", err)
	}
}

func TestDown_NegativeStepsRejected(t *testing.T) {
	// Invalid args must be rejected before the DB is touched.
	err := Down(context.Background(), config.DatabaseConfig{
		URL:          "postgres://x@x/x",
		MigrationDir: "./migrations",
	}, quietLogger(), -3)
	if err == nil {
		t.Fatal("expected error for negative steps")
	}
	if !strings.Contains(err.Error(), "steps") {
		t.Errorf("error should mention steps, got: %v", err)
	}
}

func TestSourceURLForDir(t *testing.T) {
	tmp := t.TempDir()
	got, err := sourceURLForDir(tmp)
	if err != nil {
		t.Fatalf("sourceURLForDir: %v", err)
	}
	if !strings.HasPrefix(got, "file://") {
		t.Errorf("want file:// prefix, got %q", got)
	}
	// Round-trip: the URL must contain the absolute path of the
	// directory (after ToSlash normalisation).
	abs, _ := filepath.Abs(tmp)
	if !strings.Contains(got, filepath.ToSlash(abs)) {
		t.Errorf("want url containing %q, got %q", abs, got)
	}
}

func TestSourceURLForDir_Relative(t *testing.T) {
	// Relative paths are turned into absolute paths; passing "." is
	// the common case for repo-relative MigrationDir defaults.
	got, err := sourceURLForDir(".")
	if err != nil {
		t.Fatalf("sourceURLForDir: %v", err)
	}
	if !strings.HasPrefix(got, "file://") {
		t.Errorf("want file:// prefix, got %q", got)
	}
}

// --- Integration tests below: require a real Postgres + migrations dir ---

// countMigrations returns the number of up-migration files in dir. The
// canonical migration runner picks them up in lexical order, so this
// is the version we expect Status() to report after a successful Run().
func countMigrations(t *testing.T, dir string) uint {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no *.up.sql files found in %s", dir)
	}
	return uint(len(matches)) //nolint:gosec // file count, can't overflow
}

func TestRun_IntegrationApplyAndRollback(t *testing.T) {
	dsn := dsnFromEnv(t)
	dir := repoMigrationsDir(t)

	cfg := config.DatabaseConfig{
		URL:          dsn,
		MigrationDir: dir,
	}
	ctx := context.Background()

	// We expect Run() to apply every *.up.sql in /migrations. The exact
	// count grows over time as new migrations land; reading it from the
	// directory keeps the test honest without hard-coding a number.
	wantTop := countMigrations(t, dir)

	// Up from scratch.
	if err := Run(ctx, cfg, quietLogger()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Idempotent: a second Run is a no-op.
	if err := Run(ctx, cfg, quietLogger()); err != nil {
		t.Fatalf("Run (second): %v", err)
	}

	// Status should report the latest version, not dirty.
	cur, dirty, err := Status(ctx, cfg, quietLogger())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if cur != wantTop {
		t.Errorf("current version: got %d, want %d (= #migrations in dir)", cur, wantTop)
	}
	if dirty {
		t.Errorf("schema should not be dirty after a clean Run")
	}

	// Roll back one step.
	if err := Down(ctx, cfg, quietLogger(), 1); err != nil {
		t.Fatalf("Down(1): %v", err)
	}

	// After rollback, status reports one version below the top.
	cur, _, err = Status(ctx, cfg, quietLogger())
	if err != nil {
		t.Fatalf("Status after Down: %v", err)
	}
	if cur != wantTop-1 {
		t.Errorf("after Down(1) current: got %d, want %d", cur, wantTop-1)
	}

	// Re-apply to leave the DB in a known-good state for follow-on tests.
	if err := Run(ctx, cfg, quietLogger()); err != nil {
		t.Fatalf("Run (re-apply): %v", err)
	}
}
