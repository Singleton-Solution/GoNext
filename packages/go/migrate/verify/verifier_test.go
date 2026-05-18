package verify

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/importer"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// ---------------------------------------------------------------------------
// Unit cover: nil arg handling, source factory contract.
// ---------------------------------------------------------------------------

func TestVerifier_Run_NilVerifier(t *testing.T) {
	var v *Verifier
	r, err := v.Run(context.Background())
	if err == nil || !errors.Is(err, ErrVerify) {
		t.Errorf("Run on nil verifier: err=%v want ErrVerify", err)
	}
	if r == nil {
		t.Errorf("Run on nil verifier: Report should never be nil")
	}
}

func TestVerifier_Run_NilDB(t *testing.T) {
	v := &Verifier{SourceReader: func() (io.Reader, error) { return strings.NewReader(""), nil }}
	_, err := v.Run(context.Background())
	if err == nil || !errors.Is(err, ErrVerify) {
		t.Errorf("Run with nil DB: err=%v want ErrVerify", err)
	}
}

func TestVerifier_Run_NilSourceReader(t *testing.T) {
	v := &Verifier{DB: &pgxpool.Pool{}}
	_, err := v.Run(context.Background())
	if err == nil || !errors.Is(err, ErrVerify) {
		t.Errorf("Run with nil SourceReader: err=%v want ErrVerify", err)
	}
}

// ---------------------------------------------------------------------------
// Integration tests against testcontainers Postgres.
// ---------------------------------------------------------------------------

// TestVerifier_Run_RoundTrip_TenPosts is the happy-path proof: a
// fresh import of the 10-post fixture, followed by Verify, yields
// fidelity 1.0 with zero failures.
func TestVerifier_Run_RoundTrip_TenPosts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	fixture := testdataPath(t, "ten_posts.xml")

	// 1. Import.
	imp := importer.New(pool, importer.Options{OnConflict: importer.ConflictSkip})
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := imp.Run(context.Background(), f); err != nil {
		t.Fatalf("import: %v", err)
	}

	// 2. Verify.
	v := &Verifier{
		DB: pool,
		SourceReader: func() (io.Reader, error) {
			return os.Open(fixture)
		},
	}
	report, err := v.Run(context.Background())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Fidelity < 0.999 {
		t.Errorf("fidelity: got %v want >= 0.999", report.Fidelity)
		for _, f := range report.Failures {
			t.Logf("  failure: %+v", f)
		}
	}
	if report.HasErrors() {
		t.Errorf("HasErrors: got true, want false (have %d failures)", len(report.Failures))
	}

	ok, gateErr := Gate{MinFidelity: 0.95}.Decide(report)
	if !ok || gateErr != nil {
		t.Errorf("Gate.Decide: ok=%v err=%v", ok, gateErr)
	}
}

// TestVerifier_Run_Tamper_PostDeleted simulates an out-of-band
// delete: a post that the importer wrote is gone by the time the
// verifier looks for it.
func TestVerifier_Run_Tamper_PostDeleted(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	fixture := testdataPath(t, "ten_posts.xml")
	importFixture(t, pool, fixture)

	// Delete one post out of band.
	if _, err := pool.Exec(context.Background(), `
		DELETE FROM posts WHERE slug = 'post-five'::citext
	`); err != nil {
		t.Fatalf("delete: %v", err)
	}

	report := mustVerify(t, pool, fixture)
	if !report.HasErrors() {
		t.Errorf("expected error-severity failures after delete, got none")
	}
	if !hasFailure(report, "posts.exists", SeverityError) &&
		!hasFailure(report, "posts.count", SeverityError) {
		t.Errorf("expected posts.exists or posts.count failure, got %+v", report.Failures)
	}
}

// TestVerifier_Run_Tamper_TitleMutated catches an out-of-band
// title mutation. The post is still there, but with the wrong
// content.
func TestVerifier_Run_Tamper_TitleMutated(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	fixture := testdataPath(t, "ten_posts.xml")
	importFixture(t, pool, fixture)

	// Mutate one title out of band.
	if _, err := pool.Exec(context.Background(), `
		UPDATE posts SET title = 'Tampered Title' WHERE slug = 'post-three'::citext
	`); err != nil {
		t.Fatalf("update: %v", err)
	}

	report := mustVerify(t, pool, fixture)
	if !hasFailure(report, "posts.title", SeverityError) {
		t.Errorf("expected posts.title error failure, got %+v", report.Failures)
	}
}

// TestVerifier_Run_Tamper_CommentSubtreeCollapse simulates a
// moderator collapsing a 3-deep thread: we delete the deepest
// comment so the depth histogram loses a level.
func TestVerifier_Run_Tamper_CommentSubtreeCollapse(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, cleanup := mustPostgresWithSchema(t)
	defer cleanup()

	fixture := testdataPath(t, "ten_posts.xml")
	importFixture(t, pool, fixture)

	// Drop the deepest comment (Carol's reply, depth 3) on post one.
	if _, err := pool.Exec(context.Background(), `
		DELETE FROM comments
		 WHERE author_name = 'Carol'
	`); err != nil {
		t.Fatalf("delete comment: %v", err)
	}

	report := mustVerify(t, pool, fixture)
	if !hasFailure(report, "comments.count", SeverityError) &&
		!hasFailure(report, "comments.path", SeverityError) {
		t.Errorf("expected comments.count or comments.path failure, got %+v", report.Failures)
	}
}

// TestVerifier_Run_Gate_BoundaryFidelity wires the round-trip test
// to the gate to demonstrate the issue brief's matrix: 0.96 → ok,
// 0.94 → err.
func TestVerifier_Run_Gate_BoundaryFidelity(t *testing.T) {
	// 0.96 / 0.94 cases are unit-testable on a synthetic report;
	// no DB needed. (TestGate_Decide_Pass and _Fail cover the
	// integration of these into Gate.Decide.)
	pass := &Report{ChecksTotal: 100, Passed: 96, Failed: 4}
	pass.Finalize()
	if ok, err := (Gate{MinFidelity: 0.95}).Decide(pass); !ok || err != nil {
		t.Errorf("fidelity 0.96: ok=%v err=%v", ok, err)
	}

	fail := &Report{ChecksTotal: 100, Passed: 94, Failed: 6}
	fail.Finalize()
	if ok, err := (Gate{MinFidelity: 0.95}).Decide(fail); ok || err == nil {
		t.Errorf("fidelity 0.94: ok=%v err=%v", ok, err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func testdataPath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "testdata", name)
}

// importFixture runs the importer against a fixture, used as a
// setup step for tamper tests.
func importFixture(t *testing.T, pool *pgxpool.Pool, path string) {
	t.Helper()
	imp := importer.New(pool, importer.Options{OnConflict: importer.ConflictSkip})
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	report, err := imp.Run(context.Background(), f)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("import errors: %+v", report.Errors)
	}
}

// mustVerify drives the Verifier against the fixture and returns
// the Report. Failures inside Run are t.Fatal'd; per-record
// failures land on the report for the caller to inspect.
func mustVerify(t *testing.T, pool *pgxpool.Pool, path string) *Report {
	t.Helper()
	v := &Verifier{
		DB: pool,
		SourceReader: func() (io.Reader, error) {
			return os.Open(path)
		},
	}
	report, err := v.Run(context.Background())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return report
}

// hasFailure reports whether the Report contains at least one
// failure with the given (CheckName, Severity).
func hasFailure(r *Report, check string, sev Severity) bool {
	for _, f := range r.Failures {
		if f.CheckName == check && f.Severity == sev {
			return true
		}
	}
	return false
}

// mustPostgresWithSchema spins up a Postgres container and applies
// every up-migration in the repo. Mirrors the importer's helper.
func mustPostgresWithSchema(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	mustApplyMigrations(t, dsn)
	return pool, func() { pool.Close() }
}

// mustApplyMigrations walks the repo's migrations directory and
// applies the up files in order. Uses database/sql so the call
// doesn't bring in golang-migrate (avoiding a circular import
// with the importer's own tests).
func mustApplyMigrations(t *testing.T, dsn string) {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "migrations")
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

// repoRoot walks up from this file until it finds go.work.
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

// keep imports referenced.
var _ = bytes.NewReader
