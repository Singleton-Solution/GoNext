package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// quietLogger returns a slog logger that discards all output.
// Tests that don't care about log lines use this.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// dsnFromEnv returns a usable DATABASE_URL for integration tests.
// We honor the same env var the production loader does. CI sets this
// via the postgres service; local devs set it via docker-compose.
//
// If unset, integration tests skip — they don't fail and don't try to
// fall back to a hard-coded localhost. CI will fail the unit cohort
// either way, but the contributor running `go test ./...` without a
// running Postgres should see a SKIP, not an unexplained failure.
func dsnFromEnv(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return dsn
}

func TestNew_RequiresURL(t *testing.T) {
	// Unit test, no DB needed. Verify that an empty URL surfaces an
	// immediate error, not a later panic.
	_, err := New(context.Background(), config.DatabaseConfig{URL: ""}, quietLogger())
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL, got: %v", err)
	}
}

func TestNew_MalformedURL(t *testing.T) {
	_, err := New(context.Background(), config.DatabaseConfig{
		URL: "not a valid dsn",
	}, quietLogger())
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse, got: %v", err)
	}
}

func TestNew_DSNNotLeakedInError(t *testing.T) {
	// A DSN can contain a password. The error returned by New must NOT
	// echo the DSN back — operators paste error messages into chat all
	// the time.
	const secret = "hunter2-very-secret-password"
	dsn := "postgres://user:" + secret + "@nope.invalid:9999/db?connect_timeout=1"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := New(ctx, config.DatabaseConfig{URL: dsn}, quietLogger())
	if err == nil {
		t.Fatal("expected ping to fail against bogus host")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks password: %v", err)
	}
}

// --- Integration tests below: require a real Postgres ---

func TestNew_IntegrationPing(t *testing.T) {
	dsn := dsnFromEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := New(ctx, config.DatabaseConfig{
		URL:              dsn,
		MaxOpenConns:     5,
		MaxIdleConns:     1,
		ConnMaxLifetime:  5 * time.Minute,
		ConnMaxIdleTime:  30 * time.Second,
		StatementTimeout: 5 * time.Second,
	}, quietLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pool.Close()

	var n int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if n != 1 {
		t.Errorf("got %d, want 1", n)
	}
}

func TestNew_IntegrationStatementTimeoutEnforced(t *testing.T) {
	dsn := dsnFromEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 100ms statement_timeout. pg_sleep(2) is 2 seconds — must error.
	pool, err := New(ctx, config.DatabaseConfig{
		URL:              dsn,
		MaxOpenConns:     2,
		StatementTimeout: 100 * time.Millisecond,
	}, quietLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pool.Close()

	_, err = pool.Exec(ctx, "SELECT pg_sleep(2)")
	if err == nil {
		t.Fatal("expected statement_timeout to abort pg_sleep(2)")
	}
	// pgx surfaces this as a "canceling statement due to statement timeout"
	// PgError with code 57014. We just check that an error came back —
	// the wording is platform-specific.
	if !strings.Contains(strings.ToLower(err.Error()), "statement") &&
		!strings.Contains(strings.ToLower(err.Error()), "timeout") &&
		!strings.Contains(strings.ToLower(err.Error()), "canceling") {
		t.Logf("warning: error wording unexpected: %v", err)
	}
}

func TestNew_IntegrationPoolLimits(t *testing.T) {
	dsn := dsnFromEnv(t)

	pool, err := New(context.Background(), config.DatabaseConfig{
		URL:          dsn,
		MaxOpenConns: 3,
		MaxIdleConns: 1,
	}, quietLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pool.Close()

	if got := pool.Config().MaxConns; got != 3 {
		t.Errorf("MaxConns: got %d, want 3", got)
	}
	if got := pool.Config().MinConns; got != 1 {
		t.Errorf("MinConns: got %d, want 1", got)
	}
}

// helper: the only sanity check we actually exercise here is that we
// never accidentally suppress real errors. errors.Is is fine to import
// because we don't have one to test against yet — leave a sentinel
// for future package-error tests.
var _ = errors.Is
