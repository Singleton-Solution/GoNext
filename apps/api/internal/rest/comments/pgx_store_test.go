package comments

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// mustPostgresWithCommentsSchema spins up Postgres and applies
// migrations 000001..000006 — the comments schema plus its
// dependencies. Mirrors the admin/comments helper rather than
// importing it so both packages keep an independent test substrate.
func mustPostgresWithCommentsSchema(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
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
	t.Cleanup(pool.Close)
	mustApplyCommentsMigrations(t, dsn)
	return pool
}

func mustApplyCommentsMigrations(t *testing.T, dsn string) {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "migrations")
	matches, err := filepath.Glob(filepath.Join(dir, "00000[1-6]_*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no migrations in %s", dir)
	}
	sort.Strings(matches)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
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

func seedUser(t *testing.T, pool *pgxpool.Pool, handle, display string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, handle, display_name)
		 VALUES ($1::citext, $2::citext, $3) RETURNING id`,
		handle+"@example.com", handle, display,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedPost(t *testing.T, pool *pgxpool.Pool, authorID uuid.UUID, title, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO posts (post_type, author_id, status, title, slug)
		 VALUES ('post', $1, 'published', $2, $3::citext)
		 RETURNING id`,
		authorID, title, slug,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed post: %v", err)
	}
	return id
}

// TestPgxStore_PostExists exercises both branches of the gate the
// submit handler relies on.
func TestPgxStore_PostExists(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice_pe", "Alice")
	post := seedPost(t, pool, author, "Hello", "hello-pe")

	exists, err := store.PostExists(context.Background(), post.String())
	if err != nil {
		t.Fatalf("PostExists: %v", err)
	}
	if !exists {
		t.Errorf("PostExists for real post: got false, want true")
	}

	exists, err = store.PostExists(context.Background(), uuid.New().String())
	if err != nil {
		t.Fatalf("PostExists missing: %v", err)
	}
	if exists {
		t.Errorf("PostExists for missing: got true, want false")
	}

	// Malformed ID → false, no error (the public surface 404s on it).
	exists, err = store.PostExists(context.Background(), "not-a-uuid")
	if err != nil {
		t.Fatalf("PostExists malformed: %v", err)
	}
	if exists {
		t.Errorf("PostExists for malformed: got true, want false")
	}
}

// TestPgxStore_SubmitAndList walks the happy path: post, top-level
// comment, list returns it.
func TestPgxStore_SubmitAndList(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice_sl", "Alice")
	post := seedPost(t, pool, author, "Hi", "hi-sl")

	created, err := store.Submit(context.Background(), SubmitInput{
		PostID:          post.String(),
		AuthorName:      "Anon",
		AuthorEmail:     "anon@example.com",
		AuthorIP:        "192.0.2.1",
		AuthorUserAgent: "test",
		Content:         "first comment",
	}, StatusApproved)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if created.ID == "" {
		t.Errorf("Submit: empty ID")
	}
	if created.Path == "" {
		t.Errorf("Submit: empty path; trigger did not fire")
	}
	if created.Depth != 1 {
		t.Errorf("Submit depth: got %d want 1", created.Depth)
	}
	if created.AuthorDisplayName != "Anon" {
		t.Errorf("display_name: got %q want %q", created.AuthorDisplayName, "Anon")
	}

	res, err := store.List(context.Background(), ListFilter{PostID: post.String()})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := len(res.Comments); got != 1 {
		t.Fatalf("List: got %d want 1", got)
	}
	if res.Comments[0].ID != created.ID {
		t.Errorf("List id: got %s want %s", res.Comments[0].ID, created.ID)
	}
}

// TestPgxStore_SubmitOnlyApprovedListed verifies the status='approved'
// predicate on the list query — pending/spam are excluded.
func TestPgxStore_SubmitOnlyApprovedListed(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice_only", "Alice")
	post := seedPost(t, pool, author, "Only", "only-test")

	if _, err := store.Submit(context.Background(), SubmitInput{
		PostID:     post.String(),
		AuthorName: "A",
		Content:    "pending",
	}, StatusPending); err != nil {
		t.Fatalf("Submit pending: %v", err)
	}
	if _, err := store.Submit(context.Background(), SubmitInput{
		PostID:     post.String(),
		AuthorName: "B",
		Content:    "approved",
	}, StatusApproved); err != nil {
		t.Fatalf("Submit approved: %v", err)
	}
	if _, err := store.Submit(context.Background(), SubmitInput{
		PostID:     post.String(),
		AuthorName: "C",
		Content:    "spam",
	}, StatusSpam); err != nil {
		t.Fatalf("Submit spam: %v", err)
	}

	res, err := store.List(context.Background(), ListFilter{PostID: post.String()})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := len(res.Comments); got != 1 {
		t.Fatalf("List: got %d want 1 (only approved)", got)
	}
	if res.Comments[0].Content != "approved" {
		t.Errorf("List: got %q want 'approved'", res.Comments[0].Content)
	}
}

// TestPgxStore_SubmitThreaded threads three levels deep and asserts
// the list returns them in path-ascending order with correct depth.
func TestPgxStore_SubmitThreaded(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice_thread", "Alice")
	post := seedPost(t, pool, author, "Thread", "thread-test")

	root, err := store.Submit(context.Background(), SubmitInput{
		PostID:     post.String(),
		AuthorName: "Anon",
		Content:    "root",
	}, StatusApproved)
	if err != nil {
		t.Fatalf("Submit root: %v", err)
	}
	if root.Depth != 1 {
		t.Errorf("root depth: got %d want 1", root.Depth)
	}

	child, err := store.Submit(context.Background(), SubmitInput{
		PostID:     post.String(),
		ParentID:   root.ID,
		AuthorName: "Anon",
		Content:    "child",
	}, StatusApproved)
	if err != nil {
		t.Fatalf("Submit child: %v", err)
	}
	if child.Depth != 2 {
		t.Errorf("child depth: got %d want 2", child.Depth)
	}
	if !strings.HasPrefix(child.Path, root.Path+".") {
		t.Errorf("child path %q does not extend %q", child.Path, root.Path)
	}
	if child.ParentID != root.ID {
		t.Errorf("child parent: got %s want %s", child.ParentID, root.ID)
	}

	grand, err := store.Submit(context.Background(), SubmitInput{
		PostID:     post.String(),
		ParentID:   child.ID,
		AuthorName: "Anon",
		Content:    "grand",
	}, StatusApproved)
	if err != nil {
		t.Fatalf("Submit grand: %v", err)
	}
	if grand.Depth != 3 {
		t.Errorf("grand depth: got %d want 3", grand.Depth)
	}
	if !strings.HasPrefix(grand.Path, child.Path+".") {
		t.Errorf("grand path %q does not extend %q", grand.Path, child.Path)
	}

	// Order by path ASC: root, child, grand.
	res, err := store.List(context.Background(), ListFilter{PostID: post.String()})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := len(res.Comments); got != 3 {
		t.Fatalf("List: got %d want 3", got)
	}
	if res.Comments[0].ID != root.ID ||
		res.Comments[1].ID != child.ID ||
		res.Comments[2].ID != grand.ID {
		t.Errorf("List order: got [%s, %s, %s] want [%s, %s, %s]",
			res.Comments[0].ID, res.Comments[1].ID, res.Comments[2].ID,
			root.ID, child.ID, grand.ID)
	}
}

// TestPgxStore_SubmitParentMismatch covers the cross-post parent
// rejection.
func TestPgxStore_SubmitParentMismatch(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice_mm", "Alice")
	postA := seedPost(t, pool, author, "A", "post-a-mm")
	postB := seedPost(t, pool, author, "B", "post-b-mm")

	rootA, err := store.Submit(context.Background(), SubmitInput{
		PostID:     postA.String(),
		AuthorName: "Anon",
		Content:    "in A",
	}, StatusApproved)
	if err != nil {
		t.Fatalf("Submit root A: %v", err)
	}

	// Reply to rootA but say it's on postB — should reject.
	_, err = store.Submit(context.Background(), SubmitInput{
		PostID:     postB.String(),
		ParentID:   rootA.ID,
		AuthorName: "Anon",
		Content:    "wrong post",
	}, StatusApproved)
	if !errors.Is(err, ErrParentMismatch) {
		t.Errorf("cross-post reply: err=%v want ErrParentMismatch", err)
	}
}

// TestPgxStore_SubmitUnknownPost: posting to a non-existent post id
// must yield ErrNotFound.
func TestPgxStore_SubmitUnknownPost(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	_, err := store.Submit(context.Background(), SubmitInput{
		PostID:     uuid.New().String(),
		AuthorName: "Anon",
		Content:    "ghost",
	}, StatusApproved)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("submit unknown post: err=%v want ErrNotFound", err)
	}

	_, err = store.Submit(context.Background(), SubmitInput{
		PostID:     "not-a-uuid",
		AuthorName: "Anon",
		Content:    "ghost",
	}, StatusApproved)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("submit malformed post id: err=%v want ErrNotFound", err)
	}
}

// TestPgxStore_SubmitEmptyContent ensures the store rejects empty
// bodies even if the handler somehow lets one through.
func TestPgxStore_SubmitEmptyContent(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice_empty", "Alice")
	post := seedPost(t, pool, author, "Empty", "empty-rest")

	_, err := store.Submit(context.Background(), SubmitInput{
		PostID:     post.String(),
		AuthorName: "Anon",
		Content:    "   \t  ",
	}, StatusApproved)
	if !errors.Is(err, ErrEmptyContent) {
		t.Errorf("empty content: err=%v want ErrEmptyContent", err)
	}
}

// TestPgxStore_SubmitLoggedIn covers the author_user_id link + the
// display_name join from the users table.
func TestPgxStore_SubmitLoggedIn(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	bob := seedUser(t, pool, "bob_li", "Bob Display")
	post := seedPost(t, pool, bob, "LI", "li-test")

	created, err := store.Submit(context.Background(), SubmitInput{
		PostID:       post.String(),
		AuthorUserID: bob.String(),
		AuthorName:   "Bob Display", // matches user; handler fills this
		Content:      "logged-in comment",
	}, StatusApproved)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if created.AuthorDisplayName != "Bob Display" {
		t.Errorf("display_name: got %q want %q", created.AuthorDisplayName, "Bob Display")
	}
}

// TestPgxStore_CommentsByIP exercises the rate limiter signal.
func TestPgxStore_CommentsByIP(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice_ip", "Alice")
	post := seedPost(t, pool, author, "IP", "ip-test")

	ip := "203.0.113.5"
	for i := 0; i < 3; i++ {
		if _, err := store.Submit(context.Background(), SubmitInput{
			PostID:     post.String(),
			AuthorName: "Anon",
			Content:    "row",
			AuthorIP:   ip,
		}, StatusApproved); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	n, err := store.CommentsByIP(context.Background(), ip, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("CommentsByIP: %v", err)
	}
	if n != 3 {
		t.Errorf("count: got %d want 3", n)
	}

	// A different IP returns zero.
	n, err = store.CommentsByIP(context.Background(), "198.51.100.9", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("CommentsByIP other: %v", err)
	}
	if n != 0 {
		t.Errorf("other ip count: got %d want 0", n)
	}

	// Empty IP returns zero without touching the DB.
	n, err = store.CommentsByIP(context.Background(), "", time.Now().Add(-time.Hour))
	if err != nil || n != 0 {
		t.Errorf("empty ip: n=%d err=%v want 0/nil", n, err)
	}

	// Malformed IP returns zero (we don't error out at the store).
	n, err = store.CommentsByIP(context.Background(), "not-an-ip", time.Now())
	if err != nil || n != 0 {
		t.Errorf("malformed ip: n=%d err=%v want 0/nil", n, err)
	}
}

// TestPgxStore_ListCursorPagination walks a thread of 5 across two
// pages using the (path, id) cursor.
func TestPgxStore_ListCursorPagination(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice_cp", "Alice")
	post := seedPost(t, pool, author, "CP", "cp-test")

	ids := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		// Sleep between inserts so each comment lands in a distinct
		// millisecond. List orders by (path, id), and UUIDv7's
		// millisecond-precision timestamp dominates the id ordering;
		// within the SAME ms two ids' 62 bits of random can flip
		// relative to insertion order, which races the assertion
		// below. The sleep is the smallest change that keeps the
		// test deterministic without changing the production sort
		// key.
		if i > 0 {
			time.Sleep(2 * time.Millisecond)
		}
		c, err := store.Submit(context.Background(), SubmitInput{
			PostID:     post.String(),
			AuthorName: "Anon",
			Content:    "row",
		}, StatusApproved)
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		ids = append(ids, c.ID)
	}

	// First page (3 rows).
	first, err := store.List(context.Background(), ListFilter{
		PostID: post.String(),
		Limit:  3,
	})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(first.Comments) != 3 {
		t.Fatalf("page 1: got %d want 3", len(first.Comments))
	}
	if !first.HasNext {
		t.Errorf("page 1: HasNext=false want true")
	}

	// Cursor from last row of page 1.
	last := first.Comments[len(first.Comments)-1]
	second, err := store.List(context.Background(), ListFilter{
		PostID:    post.String(),
		Limit:     3,
		AfterPath: last.Path,
		AfterID:   last.ID,
	})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(second.Comments) != 2 {
		t.Errorf("page 2: got %d want 2", len(second.Comments))
	}
	if second.HasNext {
		t.Errorf("page 2: HasNext=true want false")
	}

	// Combined IDs from both pages match the original insertion order.
	got := append([]string{}, first.Comments[0].ID, first.Comments[1].ID, first.Comments[2].ID,
		second.Comments[0].ID, second.Comments[1].ID)
	for i, id := range ids {
		if got[i] != id {
			t.Errorf("pos %d: got %s want %s", i, got[i], id)
		}
	}
}
