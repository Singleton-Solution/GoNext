package posts_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/posts"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// Integration tests for posts.PgxAutosaveStore. They spin up a real
// Postgres container, apply the repo's migration set (so we get the
// post_autosaves table from 000016 plus its FK targets), and exercise
// the full upsert / read / sweep / concurrency paths.
//
// When Docker is unreachable, containers.Postgres skips the test —
// these are integration tests, not unit tests, and a CI environment
// without Docker should not see them fail.

// mustPostgresPool starts a Postgres container, applies every up-
// migration in order, and returns a pgxpool tied to t.Cleanup. The
// resulting database contains all of users / posts / post_locks /
// post_autosaves so the store has every FK target it might touch.
func mustPostgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	applyMigrations(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// applyMigrations runs every *.up.sql file from the repo's
// /migrations directory in lexical order. We use database/sql here so
// we don't have to introduce a circular dependency on golang-migrate;
// the migrations are written as single-statement-per-line scripts that
// Postgres accepts as one ExecContext.
func applyMigrations(t *testing.T, dsn string) {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "migrations")
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

// repoRoot walks up from this file until it finds the directory
// containing go.work — that's the repo root. Mirrors the helper in
// packages/go/migrate/importer/importer_test.go.
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

// seedUser inserts a single user with a random email/handle and
// returns its id. The autosave FK requires users(id) to exist; tests
// that exercise the store always go through this helper.
//
// We append a short random suffix to the email/handle so several
// seedUser calls in the same test (or in tests sharing a container,
// once that lands) do not collide on the unique constraints.
func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	var id string
	// 8 chars of UUID are enough entropy to avoid collisions within a
	// single test run, and short enough to keep the seeded email readable
	// when a failing test logs it.
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, handle, display_name)
		 VALUES (
		     $1 || '+' || substr(gen_random_uuid()::text, 1, 8) || '@example.test',
		     $1 || substr(gen_random_uuid()::text, 1, 8),
		     $1
		 )
		 RETURNING id`,
		label,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seedUser %q: %v", label, err)
	}
	return id
}

// seedPost inserts a post with the given author. Returns the post id.
// post_type='post' is the canonical content discriminator; the
// post_types table comes from migration 000003.
func seedPost(t *testing.T, pool *pgxpool.Pool, authorID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO posts (post_type, author_id, status, title, slug)
		 VALUES ('post', $1, 'draft', 'Test', 'test-'||substr(gen_random_uuid()::text, 1, 8))
		 RETURNING id`,
		authorID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seedPost: %v", err)
	}
	return id
}

// -----------------------------------------------------------------------------
// Round-trip
// -----------------------------------------------------------------------------

// TestPgxAutosaveStore_SaveReadRoundTrip covers the canonical happy
// path: Put then Get returns the same blocks. This is the smoke test
// every other test depends on — if this fails, nothing else is
// meaningful.
func TestPgxAutosaveStore_SaveReadRoundTrip(t *testing.T) {
	pool := mustPostgresPool(t)
	store := posts.NewPgxAutosaveStore(pool)

	userID := seedUser(t, pool, "u1")
	postID := seedPost(t, pool, userID)
	blocks := json.RawMessage(`[{"type":"core/paragraph","attributes":{"text":"hi"}}]`)

	ctx := context.Background()
	saved, err := store.Put(ctx, postID, userID, blocks)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if saved.PostID != postID || saved.UserID != userID {
		t.Errorf("saved identity = %+v, want post=%s user=%s", saved, postID, userID)
	}
	if saved.UpdatedAt.IsZero() {
		t.Error("saved.UpdatedAt is zero")
	}

	got, err := store.Get(ctx, postID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// JSONB normalizes whitespace, so we compare semantically rather
	// than byte-for-byte. The semantic-equality check is the contract
	// the editor cares about; the editor never round-trips the literal
	// bytes the client posted.
	if !jsonEqual(t, got.Blocks, blocks) {
		t.Errorf("Get blocks = %s, want semantically %s", got.Blocks, blocks)
	}
	if !got.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Errorf("Get UpdatedAt = %s, want %s", got.UpdatedAt, saved.UpdatedAt)
	}
}

// jsonEqual compares two raw JSON values for semantic equality. JSONB
// reformats whitespace and re-orders object keys, so a byte-equal
// assertion would be too strict for round-trip tests against
// Postgres.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("jsonEqual: unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("jsonEqual: unmarshal b: %v", err)
	}
	ax, err := json.Marshal(x)
	if err != nil {
		t.Fatalf("jsonEqual: marshal x: %v", err)
	}
	by, err := json.Marshal(y)
	if err != nil {
		t.Fatalf("jsonEqual: marshal y: %v", err)
	}
	return string(ax) == string(by)
}

// TestPgxAutosaveStore_Get_NotFound asserts the not-found sentinel
// reaches callers as posts.ErrNotFound (so the handler can return 204).
func TestPgxAutosaveStore_Get_NotFound(t *testing.T) {
	pool := mustPostgresPool(t)
	store := posts.NewPgxAutosaveStore(pool)

	userID := seedUser(t, pool, "u_nf")
	postID := seedPost(t, pool, userID)

	_, err := store.Get(context.Background(), postID, userID)
	if !errors.Is(err, posts.ErrNotFound) {
		t.Fatalf("Get on empty: err = %v, want ErrNotFound", err)
	}
}

// -----------------------------------------------------------------------------
// Conflict resolution
// -----------------------------------------------------------------------------

// TestPgxAutosaveStore_ConflictLatestWins exercises the ON CONFLICT
// path: a second Put for the same (post, user) overwrites the first.
// The schema's PK on (post_id, user_id) is what makes this the only
// possible semantic; we assert that fact at the API layer.
func TestPgxAutosaveStore_ConflictLatestWins(t *testing.T) {
	pool := mustPostgresPool(t)
	store := posts.NewPgxAutosaveStore(pool)

	userID := seedUser(t, pool, "u_cf")
	postID := seedPost(t, pool, userID)
	ctx := context.Background()

	first := json.RawMessage(`[{"type":"core/paragraph","attributes":{"text":"first"}}]`)
	firstSaved, err := store.Put(ctx, postID, userID, first)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	// Sleep a beat so the second updated_at is strictly later — the
	// CI clock has plenty of resolution, but we want to assert "newer"
	// rather than ">=" so the test is unambiguous.
	time.Sleep(10 * time.Millisecond)

	second := json.RawMessage(`[{"type":"core/paragraph","attributes":{"text":"second"}}]`)
	secondSaved, err := store.Put(ctx, postID, userID, second)
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if !secondSaved.UpdatedAt.After(firstSaved.UpdatedAt) {
		t.Errorf("second updated_at = %s, not after first = %s",
			secondSaved.UpdatedAt, firstSaved.UpdatedAt)
	}

	got, err := store.Get(ctx, postID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !jsonEqual(t, got.Blocks, second) {
		t.Errorf("Get blocks = %s, want semantically %s", got.Blocks, second)
	}

	// And the schema-level guarantee: exactly one row exists for
	// (post, user) — the conflict didn't append.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM post_autosaves WHERE post_id = $1 AND user_id = $2`,
		postID, userID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

// -----------------------------------------------------------------------------
// Sweep
// -----------------------------------------------------------------------------

// TestPgxAutosaveStore_Sweep verifies that the TTL sweep removes only
// rows older than the threshold and leaves fresher rows intact. We
// pre-age one row by writing past updated_at directly with SQL — the
// store's Put always stamps now(), so we can't get a stale row
// through the public API.
func TestPgxAutosaveStore_Sweep(t *testing.T) {
	pool := mustPostgresPool(t)
	store := posts.NewPgxAutosaveStore(pool)
	ctx := context.Background()

	userID := seedUser(t, pool, "u_sw")
	oldPost := seedPost(t, pool, userID)
	freshPost := seedPost(t, pool, userID)

	// Old autosave: 8 days behind (past the 7-day TTL).
	if _, err := pool.Exec(ctx,
		`INSERT INTO post_autosaves (post_id, user_id, blocks, updated_at)
		 VALUES ($1, $2, '[]'::jsonb, now() - interval '8 days')`,
		oldPost, userID,
	); err != nil {
		t.Fatalf("seed old row: %v", err)
	}
	// Fresh autosave: just now.
	if _, err := store.Put(ctx, freshPost, userID, json.RawMessage(`[]`)); err != nil {
		t.Fatalf("seed fresh row: %v", err)
	}

	// Sweep with the same 7-day threshold the cron uses.
	threshold := time.Now().Add(-7 * 24 * time.Hour)
	n, err := store.Sweep(ctx, threshold)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("Sweep deleted %d rows, want 1", n)
	}

	// The fresh row survives.
	if _, err := store.Get(ctx, freshPost, userID); err != nil {
		t.Errorf("fresh row lost: %v", err)
	}
	// The old row is gone.
	if _, err := store.Get(ctx, oldPost, userID); !errors.Is(err, posts.ErrNotFound) {
		t.Errorf("old row not deleted: %v", err)
	}

	// Sweep again is a no-op (idempotent).
	n2, err := store.Sweep(ctx, threshold)
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second Sweep deleted %d rows, want 0", n2)
	}
}

// -----------------------------------------------------------------------------
// Concurrency
// -----------------------------------------------------------------------------

// TestPgxAutosaveStore_ConcurrentSaves runs many concurrent Puts for
// the same (post, user). The PK + ON CONFLICT path means every write
// is a row-level UPDATE serialised by Postgres' MVCC machinery; the
// transaction wrapping the lock-check + upsert ensures no Put errors
// out due to concurrent modification. After the storm settles, exactly
// one row exists and Get returns one of the written payloads.
func TestPgxAutosaveStore_ConcurrentSaves(t *testing.T) {
	pool := mustPostgresPool(t)
	store := posts.NewPgxAutosaveStore(pool)
	ctx := context.Background()

	userID := seedUser(t, pool, "u_conc")
	postID := seedPost(t, pool, userID)

	const N = 16
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct payload per goroutine so the "which one won" is
			// visible if the schema invariants were ever violated.
			payload := json.RawMessage(`[{"i":` + itoaForTest(i) + `}]`)
			if _, err := store.Put(ctx, postID, userID, payload); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Put: %v", err)
	}

	// Exactly one row in post_autosaves for this (post, user) pair.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM post_autosaves WHERE post_id = $1 AND user_id = $2`,
		postID, userID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after %d concurrent Puts = %d, want 1", N, count)
	}

	// Get returns one of the N writes — we don't care which (Postgres
	// can pick any serialisation order). The blocks value must parse
	// as a JSON array though, which we assert generically.
	got, err := store.Get(ctx, postID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var arr []any
	if err := json.Unmarshal(got.Blocks, &arr); err != nil {
		t.Fatalf("blocks unmarshal: %v (raw=%s)", err, got.Blocks)
	}
}

// itoaForTest is a tiny strconv.Itoa shim used only by the
// concurrency test's payload builder. Inlining strconv would pull a
// global import in for one call site; the helper keeps the file's
// imports tidy.
func itoaForTest(i int) string {
	if i == 0 {
		return "0"
	}
	var (
		buf [20]byte
		pos = len(buf)
		n   = i
	)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
