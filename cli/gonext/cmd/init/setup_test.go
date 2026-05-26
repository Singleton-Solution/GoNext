package initcmd

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// TestSetup_BadDSN_FailsFastAtConnect proves that a wrong DSN
// short-circuits in the connect step with a clean stepFailure rather
// than a panic. We deliberately don't need a real Postgres for this
// path — pgx will refuse to ping a port nothing listens on.
func TestSetup_BadDSN_FailsFastAtConnect(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := Setup(ctx, SetupOptions{
		// 1 == TCP port reserved for "tcpmux"; effectively no listener.
		// pgx will fail the dial. Even if something miraculously
		// answers, it won't speak the Postgres protocol.
		DSN:           "postgres://nobody:nopass@127.0.0.1:1/nodb?connect_timeout=2&sslmode=disable",
		Pepper:        []byte("pepperpepperpepper"),
		AdminEmail:    "a@example.com",
		AdminPassword: "verylongpassword",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if failedStep(err) != "connect" {
		t.Errorf("failedStep=%q want connect; err=%v", failedStep(err), err)
	}
}

func TestSetup_RequiresDSN(t *testing.T) {
	t.Parallel()
	_, err := Setup(context.Background(), SetupOptions{
		Pepper:        []byte("p"),
		AdminEmail:    "a@example.com",
		AdminPassword: "verylongpassword",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if failedStep(err) != "config" {
		t.Errorf("failedStep=%q want config", failedStep(err))
	}
}

func TestSetup_RequiresPepper(t *testing.T) {
	t.Parallel()
	_, err := Setup(context.Background(), SetupOptions{
		DSN:           "postgres://x@x/x",
		AdminEmail:    "a@example.com",
		AdminPassword: "verylongpassword",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if failedStep(err) != "config" {
		t.Errorf("failedStep=%q want config", failedStep(err))
	}
}

// ---------------------------------------------------------------------------
// Integration tests below — require Docker.
// ---------------------------------------------------------------------------

// TestSetup_HappyPath_FullFlow exercises the entire orchestrator
// against a real Postgres container. We let Setup do its own migrate +
// seed, then assert on the resulting rows.
func TestSetup_HappyPath_FullFlow(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration test: testcontainers spin-up flakes on shared CI runners; runs in nightly")
	}
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	root := repoRoot(t)

	pepper := []byte("the-pepper-is-secret-and-mixed-in-with-hmac")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	opts := SetupOptions{
		DSN:           dsn,
		MigrationDir:  filepath.Join(root, "migrations"),
		ThemeDir:      t.TempDir(),
		Pepper:        pepper,
		AdminEmail:    "owner@example.com",
		AdminPassword: "init-test-password-12",
		SiteName:      "Test Site",
		SiteURL:       "https://test.example",
	}

	already, err := Setup(ctx, opts)
	if err != nil {
		t.Fatalf("Setup: %v (step=%s)", err, failedStep(err))
	}
	if already {
		t.Fatal("first run reported alreadyDone=true; expected false")
	}

	// Assert: schema applied (users table exists).
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Admin row.
	var (
		adminID    string
		adminEmail string
		meta       string
	)
	err = pool.QueryRow(ctx, `
		SELECT id::text, email::text, meta::text
		FROM users WHERE email = $1::citext
	`, "owner@example.com").Scan(&adminID, &adminEmail, &meta)
	if err != nil {
		t.Fatalf("query admin: %v", err)
	}
	if adminEmail != "owner@example.com" {
		t.Errorf("admin email=%q", adminEmail)
	}
	if !strings.Contains(meta, "super_admin") {
		t.Errorf("meta did not carry super_admin role: %q", meta)
	}

	// Password row + verifies with the same pepper.
	var hash string
	if err := pool.QueryRow(ctx, `
		SELECT password_hash FROM user_passwords WHERE user_id = $1
	`, adminID).Scan(&hash); err != nil {
		t.Fatalf("query password: %v", err)
	}
	ok, _, err := password.Verify("init-test-password-12", hash, pepper)
	if err != nil {
		t.Fatalf("password.Verify: %v", err)
	}
	if !ok {
		t.Fatal("password.Verify returned false on the just-written hash")
	}

	// Options: site name, URL, theme, completion marker.
	mustOptionEquals(t, ctx, pool, siteNameKey, "Test Site")
	mustOptionEquals(t, ctx, pool, siteURLKey, "https://test.example")
	mustOptionExists(t, ctx, pool, installationCompletedKey)
	mustOptionExists(t, ctx, pool, "core.active_theme")

	// Re-running is a no-op.
	already, err = Setup(ctx, opts)
	if err != nil {
		t.Fatalf("idempotent re-run: %v", err)
	}
	if !already {
		t.Fatal("idempotent re-run reported alreadyDone=false")
	}
}

// TestSetup_AlreadyAdmin_RerunWithoutInstallMarker tests the "the DB
// already has an admin row but no installation_completed_at" path —
// the operator manually bootstrapped before init existed. We
// pre-create the admin, then init must detect via the active_theme
// fallback and short-circuit cleanly.
func TestSetup_ExistingAdmin_ErrorsBeforeMarker(t *testing.T) {
	t.Parallel()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	root := repoRoot(t)

	pepper := []byte("the-pepper-is-secret-and-mixed-in-with-hmac")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Apply migrations directly so the schema exists, but DO NOT seed
	// (i.e., no active_theme row, no installation marker).
	applyMigrations(t, dsn, filepath.Join(root, "migrations"))

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Manually insert an admin with the same email init will try.
	hash, err := password.Hash("preexisting-password", pepper)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var existingID string
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, handle, meta)
		VALUES ('owner@example.com'::citext, 'owner'::citext, '{}'::jsonb)
		RETURNING id::text
	`).Scan(&existingID)
	if err != nil {
		t.Fatalf("seed pre-existing user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_passwords (user_id, password_hash) VALUES ($1, $2)
	`, existingID, hash); err != nil {
		t.Fatalf("seed pre-existing password: %v", err)
	}

	// Now run init — admin creation should bounce.
	opts := SetupOptions{
		DSN:            dsn,
		MigrationDir:   filepath.Join(root, "migrations"),
		ThemeDir:       t.TempDir(),
		Pepper:         pepper,
		AdminEmail:     "owner@example.com",
		AdminPassword:  "different-long-password",
		SkipMigrations: true, // already applied above
		SkipThemeSeed:  true,
	}
	_, err = Setup(ctx, opts)
	if err == nil {
		t.Fatal("expected error from Setup, got nil")
	}
	if !errors.Is(err, ErrAdminExists) {
		t.Errorf("err=%v, want ErrAdminExists", err)
	}
	if failedStep(err) != "admin" {
		t.Errorf("failedStep=%q want admin", failedStep(err))
	}
}

// TestSetup_BackfillsCompletedAtFromThemeRow tests the case where
// the install was bootstrapped by an older `migrate up`, leaving an
// active_theme row but no installation_completed_at marker. Setup
// should report already-done AND write the marker so future re-runs
// hit the explicit gate.
func TestSetup_BackfillsCompletedAtFromThemeRow(t *testing.T) {
	t.Parallel()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	root := repoRoot(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	applyMigrations(t, dsn, filepath.Join(root, "migrations"))

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Write only the active_theme row (no installation marker).
	if _, err := pool.Exec(ctx, `
		INSERT INTO options (key, value, autoload, is_protected)
		VALUES ('core.active_theme', to_jsonb('gn-hello'::text), TRUE, FALSE)
	`); err != nil {
		t.Fatalf("seed active_theme: %v", err)
	}

	opts := SetupOptions{
		DSN:            dsn,
		MigrationDir:   filepath.Join(root, "migrations"),
		Pepper:         []byte("pepperpepperpepper"),
		AdminEmail:     "owner@example.com",
		AdminPassword:  "verylongpassword",
		SkipMigrations: true,
		SkipThemeSeed:  true,
	}
	already, err := Setup(ctx, opts)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !already {
		t.Fatal("expected alreadyDone=true via active_theme fallback")
	}
	mustOptionExists(t, ctx, pool, installationCompletedKey)
}

// TestSetup_MigratesLegacyInstallationKey covers the upgrade path: a
// database previously bootstrapped by a pre-fix `gonext init` carries
// the marker under the old key ("core.installation_completed_at").
// Re-running init must (a) report alreadyDone=true, (b) write the
// canonical key, and (c) drop the legacy row. Without this, the setup
// handler (which reads only the canonical key) would falsely report
// installation_completed=false on an already-bootstrapped install.
func TestSetup_MigratesLegacyInstallationKey(t *testing.T) {
	t.Parallel()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	root := repoRoot(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	applyMigrations(t, dsn, filepath.Join(root, "migrations"))

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Pre-seed: write ONLY the legacy key. Migration 000028 (applied
	// above) would normally have moved it forward, so we re-insert it
	// post-migration to simulate the in-the-wild case of a database
	// that wrote the legacy row AFTER migration 000028 was created but
	// BEFORE the operator upgraded the CLI binary (or, equivalently,
	// any install that pre-dates migration 000028 entirely).
	const legacyValue = "2026-01-15T09:00:00Z"
	if _, err := pool.Exec(ctx, `
		DELETE FROM options
		WHERE key IN ('core.installation_completed_at',
		              'core.site.installation_completed_at')
	`); err != nil {
		t.Fatalf("clean pre-existing rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO options (key, value, autoload, is_protected)
		VALUES ('core.installation_completed_at', to_jsonb($1::text), TRUE, FALSE)
	`, legacyValue); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	opts := SetupOptions{
		DSN:            dsn,
		MigrationDir:   filepath.Join(root, "migrations"),
		Pepper:         []byte("pepperpepperpepper"),
		AdminEmail:     "owner@example.com",
		AdminPassword:  "verylongpassword",
		SkipMigrations: true,
		SkipThemeSeed:  true,
	}
	already, err := Setup(ctx, opts)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !already {
		t.Fatal("expected alreadyDone=true via legacy-key migration")
	}

	// Canonical key now exists and carries the legacy value.
	mustOptionEquals(t, ctx, pool, installationCompletedKey, legacyValue)

	// Legacy row is gone — no leftover under the old name.
	var stillExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM options WHERE key = $1)`,
		legacyInstallationCompletedKey,
	).Scan(&stillExists); err != nil {
		t.Fatalf("probe legacy: %v", err)
	}
	if stillExists {
		t.Errorf("legacy key still present after migration")
	}
}

// TestMigration000028_MovesLegacyRowForward verifies the SQL-level
// migration in isolation. We apply migrations 1..27, hand-write the
// legacy row, then run 000028 directly and assert the row was moved.
// This is the path a database that NEVER re-runs `gonext init` takes
// — the migration runner is what carries it forward.
func TestMigration000028_MovesLegacyRowForward(t *testing.T) {
	t.Parallel()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	root := repoRoot(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Apply migrations 1..27 only — leaves the schema in the pre-fix
	// state where the legacy key is what older CLIs would have written.
	migDir := filepath.Join(root, "migrations")
	pre := preMigrations(t, migDir, 27)
	applyMigrationFiles(t, dsn, pre)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	const legacyValue = "2025-09-01T00:00:00Z"
	if _, err := pool.Exec(ctx, `
		INSERT INTO options (key, value, autoload, is_protected)
		VALUES ('core.installation_completed_at', to_jsonb($1::text), TRUE, FALSE)
	`, legacyValue); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	// Apply migration 000028.
	applyMigrationFiles(t, dsn, []string{
		filepath.Join(migDir, "000028_options_installation_key_compat.up.sql"),
	})

	// Canonical row carries the legacy value.
	var got string
	if err := pool.QueryRow(ctx,
		`SELECT value #>> '{}' FROM options WHERE key = $1`,
		"core.site.installation_completed_at",
	).Scan(&got); err != nil {
		t.Fatalf("read canonical: %v", err)
	}
	if got != legacyValue {
		t.Errorf("canonical value = %q, want %q", got, legacyValue)
	}

	// Legacy row is gone.
	var stillExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM options WHERE key = $1)`,
		"core.installation_completed_at",
	).Scan(&stillExists); err != nil {
		t.Fatalf("probe legacy: %v", err)
	}
	if stillExists {
		t.Errorf("legacy row still present after 000028")
	}
}

// preMigrations returns the absolute paths of every *.up.sql up to and
// including the given last version, sorted lexically. Used by the
// migration-isolation test above to apply a deterministic prefix of
// the migration set without pulling in pkg/migrate's full machinery.
func preMigrations(t *testing.T, dir string, last int) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	var out []string
	for _, m := range matches {
		base := filepath.Base(m)
		// File names start with NNNNNN_; parse the leading six digits.
		if len(base) < 6 {
			continue
		}
		n := 0
		for i := 0; i < 6; i++ {
			c := base[i]
			if c < '0' || c > '9' {
				n = -1
				break
			}
			n = n*10 + int(c-'0')
		}
		if n <= 0 || n > last {
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		t.Fatalf("no migrations matched in %s", dir)
	}
	return out
}

// applyMigrationFiles runs the listed *.up.sql files in argument order
// against dsn. A thin wrapper around the same connect-and-exec loop
// applyMigrations uses; kept separate so callers can drive a specific
// subset (e.g. "everything up to 27 plus 28 itself").
func applyMigrationFiles(t *testing.T, dsn string, files []string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, m := range files {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(m), err)
		}
	}
}

// applyMigrations is a stripped-down version of the importer's
// mustApplyMigrations: same idea, lives in this package to keep test
// isolation.
func applyMigrations(t *testing.T, dsn, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no migrations found in %s", dir)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(m), err)
		}
	}
}

func mustOptionEquals(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `
		SELECT value #>> '{}' FROM options WHERE key = $1
	`, key).Scan(&got); err != nil {
		t.Fatalf("query option %q: %v", key, err)
	}
	if got != want {
		t.Errorf("option %q = %q, want %q", key, got, want)
	}
}

func mustOptionExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM options WHERE key = $1)`, key,
	).Scan(&exists); err != nil {
		t.Fatalf("probe option %q: %v", key, err)
	}
	if !exists {
		t.Errorf("option %q missing", key)
	}
}

// repoRoot walks up from this file looking for go.work — the repo root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate repo root (go.work)")
	return ""
}
