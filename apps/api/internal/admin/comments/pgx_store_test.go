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

// mustPostgresWithCommentsSchema spins up a Postgres container, applies
// every migration in /migrations through 000006, and returns a pool
// alongside a cleanup hook.
//
// The helper applies migrations 000001..000006 in order — comments
// depends on users (000002) and posts (000004), so we need the full
// chain. We deliberately stop at 000006 to keep the test schema lean;
// later migrations bring features the comments store doesn't touch and
// would only slow the container's startup.
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

// mustApplyCommentsMigrations applies migrations 000001 through 000006
// against the supplied DSN. We use database/sql + the pgx stdlib so
// the migrations can be applied as one statement-block each, the same
// way the importer's test helper does.
func mustApplyCommentsMigrations(t *testing.T, dsn string) {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "migrations")
	matches, err := filepath.Glob(filepath.Join(dir, "00000[1-6]_*.up.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no migrations found in %s", dir)
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

// repoRoot walks up until it finds the directory containing go.work.
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

// seedUser inserts a user row and returns its UUID. We need a real
// users row for the author_user_id FK on comment inserts that exercise
// the joined display_name path.
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

// seedPost inserts a posts row and returns its UUID. The post is
// published with a stable title so the admin list's joined post_title
// is non-empty in assertions.
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

// seedComment inserts a row directly via SQL — used to populate
// fixtures without going through the Store. The path is materialised
// by the comments_set_path trigger.
func seedComment(t *testing.T, pool *pgxpool.Pool, postID uuid.UUID, parentID *uuid.UUID, authorName, content string, status Status) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	var parent any
	if parentID != nil {
		parent = *parentID
	} else {
		parent = nil
	}
	err := pool.QueryRow(context.Background(),
		`INSERT INTO comments (post_id, parent_id, author_name, content, status)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		postID, parent, authorName, content, string(status),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	return id
}

// TestPgxStore_ListByStatus walks the most common admin filter:
// "show me everything pending". Verifies the joined post_title field
// and the count.
func TestPgxStore_ListByStatus(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	author := seedUser(t, pool, "alice", "Alice")
	post := seedPost(t, pool, author, "Hello", "hello-list")

	c1 := seedComment(t, pool, post, nil, "Bob", "first", StatusPending)
	_ = seedComment(t, pool, post, nil, "Bob", "approved", StatusApproved)
	c3 := seedComment(t, pool, post, nil, "Bob", "more pending", StatusPending)

	res, err := store.List(context.Background(), ListFilter{Status: StatusPending})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := len(res.Comments); got != 2 {
		t.Fatalf("List: got %d pending comments, want 2", got)
	}
	// Newest first: c3 should precede c1 on created_at DESC.
	if res.Comments[0].ID != c3.String() {
		t.Errorf("first id: got %s want %s", res.Comments[0].ID, c3.String())
	}
	if res.Comments[1].ID != c1.String() {
		t.Errorf("second id: got %s want %s", res.Comments[1].ID, c1.String())
	}
	// post_title joined from posts.
	if res.Comments[0].PostTitle != "Hello" {
		t.Errorf("post_title: got %q want %q", res.Comments[0].PostTitle, "Hello")
	}
}

// TestPgxStore_ListFilters covers status/post/user combinations.
func TestPgxStore_ListFilters(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	alice := seedUser(t, pool, "alice2", "Alice")
	bob := seedUser(t, pool, "bob2", "Bob")
	p1 := seedPost(t, pool, alice, "Post 1", "post-1-filter")
	p2 := seedPost(t, pool, alice, "Post 2", "post-2-filter")

	// p1 carries an anonymous pending comment and a bob-authored approved one.
	_ = seedComment(t, pool, p1, nil, "Anon", "a", StatusPending)
	bobComment := insertCommentAs(t, pool, p1, &bob, "Bob's comment", StatusApproved)
	// p2 carries one alice-authored approved comment.
	_ = insertCommentAs(t, pool, p2, &alice, "Alice", StatusApproved)

	t.Run("post filter", func(t *testing.T) {
		res, err := store.List(context.Background(), ListFilter{PostID: p1.String()})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := len(res.Comments); got != 2 {
			t.Errorf("got %d want 2", got)
		}
	})

	t.Run("user filter", func(t *testing.T) {
		res, err := store.List(context.Background(), ListFilter{UserID: bob.String()})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := len(res.Comments); got != 1 {
			t.Fatalf("got %d want 1", got)
		}
		if res.Comments[0].ID != bobComment.String() {
			t.Errorf("got id %s want %s", res.Comments[0].ID, bobComment.String())
		}
	})

	t.Run("post+status filter", func(t *testing.T) {
		res, err := store.List(context.Background(), ListFilter{
			PostID: p1.String(),
			Status: StatusApproved,
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := len(res.Comments); got != 1 {
			t.Errorf("got %d want 1", got)
		}
	})
}

// insertCommentAs is a variant of seedComment that takes a user id for
// author_user_id. The display_name joined from the users row should
// take precedence over the snapshotted author_name.
func insertCommentAs(t *testing.T, pool *pgxpool.Pool, postID uuid.UUID, authorUserID *uuid.UUID, content string, status Status) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO comments (post_id, author_user_id, content, status)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		postID, authorUserID, content, string(status),
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	return id
}

// TestPgxStore_GetAndUpdateStatus covers single-row reads + transitions.
func TestPgxStore_GetAndUpdateStatus(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	alice := seedUser(t, pool, "alice3", "Alice")
	post := seedPost(t, pool, alice, "GA", "ga-test")
	cid := seedComment(t, pool, post, nil, "Anon", "hi", StatusPending)

	got, err := store.Get(context.Background(), cid.String())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("Status before: got %q want %q", got.Status, StatusPending)
	}

	upd, err := store.UpdateStatus(context.Background(), cid.String(), StatusApproved)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if upd.Status != StatusApproved {
		t.Errorf("Status after: got %q want %q", upd.Status, StatusApproved)
	}

	// Get reflects the new status.
	again, err := store.Get(context.Background(), cid.String())
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if again.Status != StatusApproved {
		t.Errorf("Get after: status %q want %q", again.Status, StatusApproved)
	}
}

// TestPgxStore_GetNotFound exercises the 404 path.
func TestPgxStore_GetNotFound(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	_, err := store.Get(context.Background(), uuid.New().String())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get unknown: err=%v want ErrNotFound", err)
	}

	// Unparseable ID also yields ErrNotFound (collapsed at API).
	_, err = store.Get(context.Background(), "not-a-uuid")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get malformed: err=%v want ErrNotFound", err)
	}
}

// TestPgxStore_BulkAtomic covers the all-or-nothing contract — one bad
// ID rolls back the whole batch.
func TestPgxStore_BulkAtomic(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	alice := seedUser(t, pool, "alice4", "Alice")
	post := seedPost(t, pool, alice, "Bulk", "bulk-test")
	c1 := seedComment(t, pool, post, nil, "Anon", "a", StatusPending)
	c2 := seedComment(t, pool, post, nil, "Anon", "b", StatusPending)
	c3 := seedComment(t, pool, post, nil, "Anon", "c", StatusPending)

	t.Run("happy path approves all", func(t *testing.T) {
		ids := []string{c1.String(), c2.String(), c3.String()}
		out, err := store.Bulk(context.Background(), ids, StatusApproved)
		if err != nil {
			t.Fatalf("Bulk: %v", err)
		}
		if got := len(out); got != 3 {
			t.Errorf("Bulk returned %d updated, want 3", got)
		}
		for i, c := range out {
			if c.Status != StatusApproved {
				t.Errorf("row %d: status %q want %q", i, c.Status, StatusApproved)
			}
			if c.ID != ids[i] {
				t.Errorf("row %d: id %s want %s (order preserved)", i, c.ID, ids[i])
			}
		}
	})

	t.Run("one bad id rolls back", func(t *testing.T) {
		// Trash one row that exists and pair with an unknown UUID. Then
		// the whole batch should fail and the row that exists must
		// remain at status=approved (the previous test's terminal state).
		bad := uuid.New().String()
		_, err := store.Bulk(context.Background(),
			[]string{c1.String(), bad}, StatusTrash)
		if !errors.Is(err, ErrBulkPartial) {
			t.Fatalf("Bulk with bad id: err=%v want ErrBulkPartial", err)
		}
		// c1 stays approved.
		got, err := store.Get(context.Background(), c1.String())
		if err != nil {
			t.Fatalf("Get c1: %v", err)
		}
		if got.Status != StatusApproved {
			t.Errorf("c1 status after rollback: got %q want %q", got.Status, StatusApproved)
		}
	})

	t.Run("malformed uuid rejected", func(t *testing.T) {
		_, err := store.Bulk(context.Background(),
			[]string{c1.String(), "not-a-uuid"}, StatusTrash)
		if !errors.Is(err, ErrBulkPartial) {
			t.Errorf("Bulk with malformed uuid: err=%v want ErrBulkPartial", err)
		}
	})
}

// TestPgxStore_ReplyThreading is the core ltree scenario: a reply
// inherits its parent's path and the resulting thread orders correctly.
func TestPgxStore_ReplyThreading(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	alice := seedUser(t, pool, "alice5", "Alice")
	post := seedPost(t, pool, alice, "Threading", "threading-test")

	// Seed a top-level approved comment.
	root := seedComment(t, pool, post, nil, "Anon", "root", StatusApproved)
	rootRow, err := store.Get(context.Background(), root.String())
	if err != nil {
		t.Fatalf("Get root: %v", err)
	}

	// Reply to it. The path should be root.path || labelFromID(reply.id).
	reply, err := store.Reply(context.Background(),
		root.String(), alice.String(), "Mod Operator", "this is a reply")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Status != StatusApproved {
		t.Errorf("reply status: got %q want approved", reply.Status)
	}
	if reply.ParentID != root.String() {
		t.Errorf("reply parent_id: got %s want %s", reply.ParentID, root.String())
	}
	expectedPrefix := rootRow.Path + "."
	if !strings.HasPrefix(reply.Path, expectedPrefix) {
		t.Errorf("reply path %q does not start with %q", reply.Path, expectedPrefix)
	}
	if !strings.HasSuffix(reply.Path, labelFromID(reply.ID)) {
		t.Errorf("reply path %q does not end with label %q", reply.Path, labelFromID(reply.ID))
	}
	if reply.PostID != post.String() {
		t.Errorf("reply post_id: got %s want %s", reply.PostID, post.String())
	}

	// Nested reply.
	deep, err := store.Reply(context.Background(),
		reply.ID, alice.String(), "Mod Operator", "deeper")
	if err != nil {
		t.Fatalf("Reply (deep): %v", err)
	}
	if !strings.HasPrefix(deep.Path, reply.Path+".") {
		t.Errorf("deep path %q does not extend %q", deep.Path, reply.Path)
	}

	// The four-row thread: root + reply + deep, all visible on the list.
	res, err := store.List(context.Background(), ListFilter{PostID: post.String()})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := len(res.Comments); got != 3 {
		t.Errorf("post comments: got %d want 3", got)
	}
}

// TestPgxStore_ReplyNotFound exercises the parent-missing path.
func TestPgxStore_ReplyNotFound(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	_, err := store.Reply(context.Background(),
		uuid.New().String(), "", "Mod", "ignored")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Reply to missing parent: err=%v want ErrNotFound", err)
	}

	_, err = store.Reply(context.Background(),
		"bad-uuid", "", "Mod", "ignored")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Reply with malformed parent: err=%v want ErrNotFound", err)
	}
}

// TestPgxStore_ReplyEmptyContent guards the dev-fallthrough behaviour
// — even if the handler somehow lets an empty body through, the store
// should reject it.
func TestPgxStore_ReplyEmptyContent(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	alice := seedUser(t, pool, "alice6", "Alice")
	post := seedPost(t, pool, alice, "Empty", "empty-test")
	root := seedComment(t, pool, post, nil, "Anon", "ok", StatusApproved)

	_, err := store.Reply(context.Background(),
		root.String(), "", "Mod", "   \t  ")
	if !errors.Is(err, ErrEmptyContent) {
		t.Errorf("Reply with whitespace: err=%v want ErrEmptyContent", err)
	}
}

// TestPgxStore_DisplayNameJoin verifies the LEFT JOIN on users
// resolves display_name and falls back to author_name when the user
// link is absent.
func TestPgxStore_DisplayNameJoin(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	alice := seedUser(t, pool, "alice7", "Alice Display")
	post := seedPost(t, pool, alice, "Names", "names-test")

	// Linked user comment.
	linked := insertCommentAs(t, pool, post, &alice, "linked", StatusApproved)
	got, err := store.Get(context.Background(), linked.String())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AuthorDisplayName != "Alice Display" {
		t.Errorf("linked display_name: got %q want %q", got.AuthorDisplayName, "Alice Display")
	}

	// Anonymous (author_name snapshot used).
	anon := seedComment(t, pool, post, nil, "Anon Snapshot", "anon", StatusApproved)
	got, err = store.Get(context.Background(), anon.String())
	if err != nil {
		t.Fatalf("Get anon: %v", err)
	}
	if got.AuthorDisplayName != "Anon Snapshot" {
		t.Errorf("anon display_name: got %q want %q", got.AuthorDisplayName, "Anon Snapshot")
	}
}

// TestPgxStore_ListPagination walks two pages.
func TestPgxStore_ListPagination(t *testing.T) {
	pool := mustPostgresWithCommentsSchema(t)
	store := NewPgxStore(pool)

	alice := seedUser(t, pool, "alice8", "Alice")
	post := seedPost(t, pool, alice, "Page", "page-test")

	// Seed 5 comments. Pause briefly between inserts so created_at
	// strictly orders — UUID v7 already orders monotonically but the
	// list query ties on (created_at, id) so we want both keys
	// agreeing.
	for i := 0; i < 5; i++ {
		_ = seedComment(t, pool, post, nil, "Anon", "row", StatusPending)
		time.Sleep(2 * time.Millisecond)
	}

	first, err := store.List(context.Background(), ListFilter{
		Status: StatusPending,
		Limit:  3,
		Page:   1,
	})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(first.Comments) != 3 {
		t.Fatalf("page 1: got %d want 3", len(first.Comments))
	}
	if !first.HasNext {
		t.Errorf("page 1: HasNext=false, want true")
	}

	second, err := store.List(context.Background(), ListFilter{
		Status: StatusPending,
		Limit:  3,
		Page:   2,
	})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(second.Comments) != 2 {
		t.Errorf("page 2: got %d want 2", len(second.Comments))
	}
	if second.HasNext {
		t.Errorf("page 2: HasNext=true, want false")
	}
}
