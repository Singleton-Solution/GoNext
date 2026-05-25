package posts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// TestPgxStore_RoundTrip is the headline integration test for the
// Postgres-backed store: it spins up a fresh container, runs every
// migration through 000004 (and the rest, for fidelity), then exercises
// create + read + list + update + soft-delete in sequence. The chain
// shape mirrors what the REST handler actually does on a typical
// admin session — one CRUD round-trip is more useful than a dozen
// micro-tests against a substrate this slow.
func TestPgxStore_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, authorID := mustPgxStorePool(t)
	store := NewPgxStore(pool)
	ctx := context.Background()

	// -------------------------------------------------------------------
	// Create
	// -------------------------------------------------------------------
	title := "Hello, World"
	slug := "hello-world"
	status := "draft"
	blocks := json.RawMessage(`[{"type":"core/paragraph","content":"hi"}]`)
	created, err := store.Create(ctx, PostTypePost, authorID, CreateInput{
		Title:         &title,
		Slug:          &slug,
		Status:        &status,
		ContentBlocks: blocks,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("Create: returned empty id")
	}
	if created.Title != "Hello, World" {
		t.Errorf("Create: title = %q", created.Title)
	}
	if created.Version != 1 {
		t.Errorf("Create: version = %d, want 1", created.Version)
	}
	if len(created.Hash()) == 0 {
		t.Errorf("Create: hash was not populated")
	}
	if created.PostType != PostTypePost {
		t.Errorf("Create: post_type = %q, want %q", created.PostType, PostTypePost)
	}

	// -------------------------------------------------------------------
	// Get
	// -------------------------------------------------------------------
	got, err := store.Get(ctx, PostTypePost, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get: id = %q, want %q", got.ID, created.ID)
	}

	// -------------------------------------------------------------------
	// Get cross-type returns NotFound
	// -------------------------------------------------------------------
	if _, err := store.Get(ctx, PostTypePage, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get cross-type = %v, want ErrNotFound", err)
	}

	// -------------------------------------------------------------------
	// List sees the row
	// -------------------------------------------------------------------
	rows, err := store.List(ctx, PostTypePost, ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List: len = %d, want 1", len(rows))
	}
	if rows[0].ID != created.ID {
		t.Errorf("List[0].id = %q, want %q", rows[0].ID, created.ID)
	}

	// -------------------------------------------------------------------
	// Update bumps version + persists title
	// -------------------------------------------------------------------
	newTitle := "Hello, Postgres"
	updated, err := store.Update(ctx, PostTypePost, created.ID, created.Version, UpdateInput{
		Title: &newTitle,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Title != "Hello, Postgres" {
		t.Errorf("Update: title = %q", updated.Title)
	}
	if updated.Version != created.Version+1 {
		t.Errorf("Update: version = %d, want %d", updated.Version, created.Version+1)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("Update: updated_at did not advance (created %v, updated %v)", created.UpdatedAt, updated.UpdatedAt)
	}

	// -------------------------------------------------------------------
	// Stale-version Update -> ErrVersionConflict
	// -------------------------------------------------------------------
	stale := "Stale"
	if _, err := store.Update(ctx, PostTypePost, created.ID, created.Version, UpdateInput{Title: &stale}); !errors.Is(err, ErrVersionConflict) {
		t.Errorf("Update stale = %v, want ErrVersionConflict", err)
	}

	// -------------------------------------------------------------------
	// Trash (soft-delete) flips status to 'trash'
	// -------------------------------------------------------------------
	trashed, err := store.Trash(ctx, PostTypePost, created.ID, updated.Version)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if trashed.Status != "trash" {
		t.Errorf("Trash: status = %q, want trash", trashed.Status)
	}
	if trashed.Version != updated.Version+1 {
		t.Errorf("Trash: version = %d, want %d", trashed.Version, updated.Version+1)
	}

	// -------------------------------------------------------------------
	// Trash is reachable by Get (row still exists) — the schema does
	// not have a deleted_at column; soft-delete is status='trash'.
	// -------------------------------------------------------------------
	stillThere, err := store.Get(ctx, PostTypePost, created.ID)
	if err != nil {
		t.Fatalf("Get after Trash: %v", err)
	}
	if stillThere.Status != "trash" {
		t.Errorf("Get after Trash: status = %q, want trash", stillThere.Status)
	}
}

// TestPgxStore_DuplicateSlug exercises the partial-unique index in
// 000004: two non-trash posts of the same type with the same slug
// can't coexist. The store surfaces this as ErrDuplicateSlug.
func TestPgxStore_DuplicateSlug(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, authorID := mustPgxStorePool(t)
	store := NewPgxStore(pool)
	ctx := context.Background()

	title := "First"
	slug := "duplicate-slug"
	if _, err := store.Create(ctx, PostTypePost, authorID, CreateInput{Title: &title, Slug: &slug}); err != nil {
		t.Fatalf("Create #1: %v", err)
	}

	title2 := "Second"
	_, err := store.Create(ctx, PostTypePost, authorID, CreateInput{Title: &title2, Slug: &slug})
	if !errors.Is(err, ErrDuplicateSlug) {
		t.Errorf("Create dup = %v, want ErrDuplicateSlug", err)
	}
}

// TestPgxStore_NotFound covers the empty-DB read path: a Get against a
// well-formed UUID that never existed should be ErrNotFound, and the
// existence probe should agree (no "version conflict but row is gone"
// false positives).
func TestPgxStore_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, _ := mustPgxStorePool(t)
	store := NewPgxStore(pool)
	ctx := context.Background()

	const fakeID = "00000000-0000-7000-8000-000000000000" // valid UUID v7 layout, definitely not in the DB.

	if _, err := store.Get(ctx, PostTypePost, fakeID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
	if _, err := store.Update(ctx, PostTypePost, fakeID, 1, UpdateInput{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update missing = %v, want ErrNotFound", err)
	}
	if _, err := store.Trash(ctx, PostTypePost, fakeID, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("Trash missing = %v, want ErrNotFound", err)
	}
}

// TestPgxStore_TypeIsolation proves that the post/page mounts do not
// see each other's rows. A row inserted as a 'page' must not show up
// in a List(post_type='post').
func TestPgxStore_TypeIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	pool, authorID := mustPgxStorePool(t)
	store := NewPgxStore(pool)
	ctx := context.Background()

	pageTitle := "About"
	pageSlug := "about"
	if _, err := store.Create(ctx, PostTypePage, authorID, CreateInput{Title: &pageTitle, Slug: &pageSlug}); err != nil {
		t.Fatalf("Create page: %v", err)
	}

	rows, err := store.List(ctx, PostTypePost, ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List posts: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("List(post) saw %d rows, want 0", len(rows))
	}

	pageRows, err := store.List(ctx, PostTypePage, ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List pages: %v", err)
	}
	if len(pageRows) != 1 {
		t.Errorf("List(page): len = %d, want 1", len(pageRows))
	}
}

// -----------------------------------------------------------------------------
// Test substrate.
// -----------------------------------------------------------------------------

// mustPgxStorePool spins up a Postgres container, applies every up
// migration in the repo, seeds a user (the posts.author_id FK requires
// it), and returns (pool, author_id). Cleanup happens via t.Cleanup so
// even a t.Fatal in the test body releases the container.
//
// Tests get their own container — no shared state between tests, no
// cross-test contamination, and `go test -count=N` keeps working.
func mustPgxStorePool(t *testing.T) (*pgxpool.Pool, string) {
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

	// Seed a user. posts.author_id REFERENCES users(id) ON DELETE
	// RESTRICT — every Create needs a real id to point at.
	var authorID string
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, handle, meta)
		VALUES ('author@example.com'::citext, 'author'::citext, '{}'::jsonb)
		RETURNING id::text
	`).Scan(&authorID)
	if err != nil {
		t.Fatalf("seed author: %v", err)
	}
	return pool, authorID
}

// applyMigrations runs every *.up.sql in the repo's migrations
// directory, in lexicographic order, against dsn. Mirrors the helper
// that lives in cli/gonext/cmd/init/setup_test.go and the importer's
// integration tests; kept local so the apps/api test tree has no
// cross-module test dependency.
func applyMigrations(t *testing.T, dsn string) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

// repoRoot walks up from this test file looking for go.work — the
// canonical repo-root marker.
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

