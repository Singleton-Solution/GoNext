package revisions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPolicy_Normalize clamps negatives to zero. Cheap unit cover
// because every other test path takes the normalize'd value.
func TestPolicy_Normalize(t *testing.T) {
	p := Policy{KeepLast: -1, KeepWithin: -1}.normalize()
	if p.KeepLast != 0 {
		t.Errorf("KeepLast not clamped: %d", p.KeepLast)
	}
	if p.KeepWithin != 0 {
		t.Errorf("KeepWithin not clamped: %v", p.KeepWithin)
	}
}

// TestDefaultPolicy locks the documented defaults so a reckless edit
// to DefaultPolicy fails fast.
func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if p.KeepLast != 30 {
		t.Errorf("KeepLast default: got %d want 30", p.KeepLast)
	}
	if p.KeepWithin != 7*24*time.Hour {
		t.Errorf("KeepWithin default: got %v want 168h", p.KeepWithin)
	}
}

// TestPruner_EmptyStore exercises the no-revisions case. Nothing in
// the store, nothing to prune. PostsScanned should be zero.
func TestPruner_EmptyStore(t *testing.T) {
	store := NewMemoryStore()
	lister := PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return nil, nil
	})
	p := NewPruner(store, lister)

	stats, err := p.Run(context.Background(), DefaultPolicy(), PrunerOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.PostsScanned != 0 || stats.Scanned != 0 || stats.Deleted != 0 || stats.Skipped != 0 {
		t.Errorf("expected zero stats on empty store, got %+v", stats)
	}
	// Duration is non-negative; on a fast machine the empty path can
	// complete in under a nanosecond and round to zero. Just sanity-
	// check that we didn't return a negative value.
	if stats.Duration < 0 {
		t.Errorf("Duration should be >= 0, got %v", stats.Duration)
	}
}

// TestPruner_PolicyDisabledIsNoop ensures both-zero Policy short-
// circuits before touching the store. Guards against a misconfigured
// cron burning a full-table scan to delete zero rows.
func TestPruner_PolicyDisabledIsNoop(t *testing.T) {
	called := false
	lister := PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		called = true
		return nil, nil
	})
	p := NewPruner(NewMemoryStore(), lister)

	stats, err := p.Run(context.Background(), Policy{}, PrunerOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("lister should not be called when policy is fully disabled")
	}
	if len(stats.Notes) == 0 || !strings.Contains(stats.Notes[0], "policy disabled") {
		t.Errorf("expected policy-disabled note, got %v", stats.Notes)
	}
}

// TestPruner_AllRevisionsWithinKeepWindow is the "nothing to prune"
// happy path: revisions exist, but they're all newer than KeepWithin
// AND fewer than KeepLast.
func TestPruner_AllRevisionsWithinKeepWindow(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)

	// 3 manual revisions, all within the last hour. KeepLast=10,
	// KeepWithin=24h — nothing should drop.
	for i := 0; i < 3; i++ {
		mustSave(t, store, Revision{
			PostID: post, AuthorID: author, Kind: Manual,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
		})
	}

	lister := PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return []uuid.UUID{post}, nil
	})
	p := NewPruner(store, lister)
	stats, err := p.Run(context.Background(), Policy{
		KeepLast:   10,
		KeepWithin: 24 * time.Hour,
	}, PrunerOptions{NowFunc: store.NowFunc})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Deleted != 0 {
		t.Errorf("expected 0 deletes, got %d", stats.Deleted)
	}
	if stats.PostsScanned != 1 {
		t.Errorf("expected 1 post scanned, got %d", stats.PostsScanned)
	}
	if stats.Scanned != 3 {
		t.Errorf("expected 3 revisions scanned, got %d", stats.Scanned)
	}
}

// TestPruner_PermanentRevisionsNeverDeleted is the load-bearing
// is_permanent check. We seed five revisions, mark the oldest one as
// permanent, then set KeepLast=1 so the older four would normally drop.
// The permanent row must survive.
func TestPruner_PermanentRevisionsNeverDeleted(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)

	// Seed 5 revisions, with #0 marked permanent. We set IsPermanent
	// at write time so the row hits the store with the flag on.
	ids := make([]uuid.UUID, 5)
	for i := 0; i < 5; i++ {
		ids[i] = mustSave(t, store, Revision{
			PostID: post, AuthorID: author, Kind: Manual,
			IsPermanent:   i == 0,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
		})
	}

	// Make the seeded revisions "old" by advancing the store's clock
	// past the KeepWithin window before we Run.
	advance := store.now().Add(48 * time.Hour)
	store.NowFunc = func() time.Time { return advance }

	lister := PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return []uuid.UUID{post}, nil
	})
	p := NewPruner(store, lister)
	stats, err := p.Run(context.Background(), Policy{
		KeepLast:   1,
		KeepWithin: 24 * time.Hour,
	}, PrunerOptions{NowFunc: store.NowFunc})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The permanent row must still be retrievable.
	got, err := store.Get(context.Background(), ids[0])
	if err != nil {
		t.Fatalf("permanent revision deleted! Get(%s): %v", ids[0], err)
	}
	if !got.IsPermanent {
		t.Errorf("permanent flag not round-tripped on stored revision")
	}

	if stats.Skipped < 1 {
		t.Errorf("expected Skipped >= 1 (permanent row), got %d", stats.Skipped)
	}
	// And the rest of the older ones (1..3) should have been pruned,
	// keeping only the newest non-permanent revision (#4) under
	// KeepLast=1.
	if stats.Deleted < 1 {
		t.Errorf("expected some deletions of non-permanent older revisions, got %d", stats.Deleted)
	}
}

// TestPruner_DryRunMakesNoChanges verifies DryRun reports the count
// of would-be-deletions without actually removing anything.
func TestPruner_DryRunMakesNoChanges(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)
	for i := 0; i < 6; i++ {
		mustSave(t, store, Revision{
			PostID: post, AuthorID: author, Kind: Manual,
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
		})
	}
	// Move the clock so all 6 are past KeepWithin.
	advance := store.now().Add(48 * time.Hour)
	store.NowFunc = func() time.Time { return advance }

	lister := PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return []uuid.UUID{post}, nil
	})
	p := NewPruner(store, lister)
	stats, err := p.Run(context.Background(), Policy{
		KeepLast:   2,
		KeepWithin: 24 * time.Hour,
	}, PrunerOptions{DryRun: true, NowFunc: store.NowFunc})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !stats.DryRun {
		t.Errorf("Stats.DryRun should be true")
	}
	if stats.Deleted != 4 {
		t.Errorf("expected 4 would-be deletions (6-2), got %d", stats.Deleted)
	}

	// All 6 revisions must still be present.
	got, err := store.List(context.Background(), post, Filter{Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 6 {
		t.Errorf("dry-run mutated store: %d revisions remain (want 6)", len(got))
	}
}

// TestPruner_PerPostError keeps going on a per-post failure. A single
// flaky post must not wedge the nightly job.
func TestPruner_PerPostError(t *testing.T) {
	store := newSeededMemoryStore(t)
	ok1 := newPostID(t)
	bad := newPostID(t) // never has revisions; List succeeds with empty
	ok2 := newPostID(t)

	// Seed ok1 and ok2 with three revs each.
	for _, p := range []uuid.UUID{ok1, ok2} {
		for i := 0; i < 3; i++ {
			mustSave(t, store, Revision{
				PostID: p, Kind: Manual,
				ContentBlocks: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
			})
		}
	}

	// Inject a store that errors on List for `bad` only.
	wrapped := &failingListStore{Store: store, failOn: bad, failWith: errors.New("disk on fire")}
	lister := PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return []uuid.UUID{ok1, bad, ok2}, nil
	})
	p := NewPruner(wrapped, lister)

	stats, err := p.Run(context.Background(), DefaultPolicy(), PrunerOptions{})
	if err == nil {
		t.Error("expected error from bad post, got nil")
	}
	if !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("expected wrapped error containing 'disk on fire', got %v", err)
	}
	if stats.PostsScanned != 3 {
		t.Errorf("PostsScanned: got %d want 3 (kept going after bad)", stats.PostsScanned)
	}
}

// TestPruner_BatchSizeCapsWork ensures BatchSize bounds the per-Run
// post sweep. The remaining posts will be picked up by the next Run.
func TestPruner_BatchSizeCapsWork(t *testing.T) {
	store := newSeededMemoryStore(t)
	posts := []uuid.UUID{newPostID(t), newPostID(t), newPostID(t), newPostID(t)}
	for _, p := range posts {
		mustSave(t, store, Revision{
			PostID: p, Kind: Manual, ContentBlocks: json.RawMessage(`{}`),
		})
	}
	lister := PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return posts, nil
	})
	p := NewPruner(store, lister)

	stats, err := p.Run(context.Background(), DefaultPolicy(), PrunerOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.PostsScanned != 2 {
		t.Errorf("PostsScanned: got %d want 2 (BatchSize=2)", stats.PostsScanned)
	}
}

// TestPruner_ContextCancelStopsEarly verifies ctx cancellation aborts
// the sweep without panicking.
func TestPruner_ContextCancelStopsEarly(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	mustSave(t, store, Revision{
		PostID: post, Kind: Manual, ContentBlocks: json.RawMessage(`{}`),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	p := NewPruner(store, PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return []uuid.UUID{post}, nil
	}))
	_, err := p.Run(ctx, DefaultPolicy(), PrunerOptions{})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestPruner_DryRunRespectsPermanent ensures DryRun's classifier also
// honours the is_permanent flag — pinned rows show up in Skipped, not
// in Deleted.
func TestPruner_DryRunRespectsPermanent(t *testing.T) {
	store := newSeededMemoryStore(t)
	post := newPostID(t)
	author := newPostID(t)
	for i := 0; i < 4; i++ {
		mustSave(t, store, Revision{
			PostID: post, AuthorID: author, Kind: Manual,
			IsPermanent:   i == 0, // oldest pinned
			ContentBlocks: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)),
		})
	}
	advance := store.now().Add(48 * time.Hour)
	store.NowFunc = func() time.Time { return advance }

	p := NewPruner(store, PostListerFunc(func(_ context.Context) ([]uuid.UUID, error) {
		return []uuid.UUID{post}, nil
	}))
	stats, err := p.Run(context.Background(), Policy{
		KeepLast:   1,
		KeepWithin: 24 * time.Hour,
	}, PrunerOptions{DryRun: true, NowFunc: store.NowFunc})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Skipped < 1 {
		t.Errorf("Skipped should include the pinned row: %d", stats.Skipped)
	}
	// Of 4 rows, with KeepLast=1, the un-pinned older two should be
	// would-be deleted (the pinned row + the newest non-pinned survive).
	if stats.Deleted != 2 {
		t.Errorf("expected 2 dry-run deletions, got %d (stats=%+v)", stats.Deleted, stats)
	}
}

// failingListStore wraps a Store and returns failWith from List when
// asked about failOn. Lets the per-post error test drive a single bad
// post without poisoning the others.
type failingListStore struct {
	Store
	failOn   uuid.UUID
	failWith error
}

func (s *failingListStore) List(ctx context.Context, postID uuid.UUID, f Filter) ([]Revision, error) {
	if postID == s.failOn {
		return nil, s.failWith
	}
	return s.Store.List(ctx, postID, f)
}

// mustSave is a tiny helper for the Pruner suite: it Saves and fails
// the test on error. Keeps the table-driven cases readable.
//
// We force every revision to be a snapshot. The Pruner suite is
// asserting count-cap behaviour; if we let the store delta-chain the
// rows it changes what's deletable (reachability protects ancestors
// referenced by a kept delta). The snapshot-only path keeps the
// candidate set clean.
func mustSave(t *testing.T, store Store, r Revision) uuid.UUID {
	t.Helper()
	id, err := store.Save(context.Background(), r, WithForceSnapshot())
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return id
}

// TestPostgresPostLister exercises the production lister against a
// real Postgres container. Skipped automatically when Docker isn't
// available — see containers.Postgres.
func TestPostgresPostLister(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	dsn := containers.Postgres(t)
	if dsn == "" {
		return
	}
	ctx := context.Background()
	pool := mustPool(t, dsn)
	t.Cleanup(pool.Close)

	mustInitRevisionsSchema(t, dsn)

	// Empty table: lister returns nil.
	l := NewPostgresPostLister(pool)
	posts, err := l.ListPostsWithRevisions(ctx)
	if err != nil {
		t.Fatalf("ListPostsWithRevisions: %v", err)
	}
	if len(posts) != 0 {
		t.Errorf("empty table: got %d posts, want 0", len(posts))
	}

	// Seed two posts with one revision each.
	p1 := uuid.New()
	p2 := uuid.New()
	for _, p := range []uuid.UUID{p1, p2, p1 /* dup */} {
		if _, ierr := pool.Exec(ctx, `INSERT INTO post_revisions (post_id, kind, snapshot) VALUES ($1, 'manual', '{}'::jsonb)`, p); ierr != nil {
			t.Fatalf("insert: %v", ierr)
		}
	}

	posts, err = l.ListPostsWithRevisions(ctx)
	if err != nil {
		t.Fatalf("ListPostsWithRevisions: %v", err)
	}
	if len(posts) != 2 {
		t.Errorf("got %d distinct posts, want 2", len(posts))
	}
}

// TestPruner_Postgres_EndToEnd seeds a real Postgres database, runs
// the Pruner against it, and asserts the survivor set. Skipped if
// Docker isn't available.
func TestPruner_Postgres_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	dsn := containers.Postgres(t)
	if dsn == "" {
		return
	}
	ctx := context.Background()
	pool := mustPool(t, dsn)
	t.Cleanup(pool.Close)
	mustInitRevisionsSchema(t, dsn)

	store := NewPostgresStore(pool)
	// Pin "now" so KeepWithin math is deterministic.
	fixedNow := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	store.NowFunc = func() time.Time { return fixedNow }

	post := uuid.New()
	// Insert 5 manual revisions, oldest 30 days ago. We bypass
	// store.Save so we can pin created_at directly.
	revIDs := make([]uuid.UUID, 5)
	for i := 0; i < 5; i++ {
		id := uuid.New()
		revIDs[i] = id
		createdAt := fixedNow.Add(time.Duration(-30+i) * 24 * time.Hour)
		// Oldest revision is pinned permanent.
		isPerm := i == 0
		_, err := pool.Exec(ctx, `
			INSERT INTO post_revisions
				(id, post_id, created_at, kind, snapshot, is_permanent)
			VALUES ($1, $2, $3, 'manual', '{}'::jsonb, $4)
		`, id, post, createdAt, isPerm)
		if err != nil {
			t.Fatalf("insert revision %d: %v", i, err)
		}
	}

	lister := NewPostgresPostLister(pool)
	p := NewPruner(store, lister)
	stats, err := p.Run(ctx, Policy{
		KeepLast:   2,
		KeepWithin: 24 * time.Hour, // only #4 is within the window
	}, PrunerOptions{NowFunc: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.PostsScanned != 1 {
		t.Errorf("PostsScanned: got %d want 1", stats.PostsScanned)
	}
	if stats.Deleted == 0 {
		t.Errorf("expected at least one deletion, got %d", stats.Deleted)
	}

	// Permanent row #0 must still exist.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_revisions WHERE id = $1`, revIDs[0]).Scan(&n); err != nil {
		t.Fatalf("count permanent: %v", err)
	}
	if n != 1 {
		t.Errorf("permanent revision was deleted! count=%d", n)
	}
}

// TestPostgresStore_PruneLocked_ConcurrentDoesNotDoubleDelete is the
// concurrency test from the issue brief: two PruneLocked calls in
// parallel must partition the deletable rows, not stomp on each
// other. We seed a large enough candidate set that the timing window
// is real, then run two prunes from separate goroutines.
func TestPostgresStore_PruneLocked_ConcurrentDoesNotDoubleDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	dsn := containers.Postgres(t)
	if dsn == "" {
		return
	}
	ctx := context.Background()
	pool := mustPool(t, dsn)
	t.Cleanup(pool.Close)
	mustInitRevisionsSchema(t, dsn)

	store := NewPostgresStore(pool)
	fixedNow := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	store.NowFunc = func() time.Time { return fixedNow }

	post := uuid.New()
	const total = 40
	for i := 0; i < total; i++ {
		// All revisions 30 days old so every one is outside the
		// KeepWithin window and eligible for the count cap.
		createdAt := fixedNow.Add(time.Duration(-30*24+i) * time.Hour)
		_, err := pool.Exec(ctx, `
			INSERT INTO post_revisions
				(post_id, created_at, kind, snapshot)
			VALUES ($1, $2, 'manual', '{}'::jsonb)
		`, post, createdAt)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	policy := RetentionPolicy{
		MaxManual:  5, // keep 5 most recent → 35 candidates
		MinKeepAll: 0,
	}

	// Two concurrent PruneLocked calls. Both target the same post,
	// so all candidates are FOR UPDATE'd in one race. The
	// SKIP LOCKED clause partitions the rows; the union of deletions
	// MUST equal what a single call would have produced (i.e. 35),
	// and no row is deleted twice (DELETE on a missing row returns
	// 0 rows affected — so RowsAffected double-counting cannot happen
	// by accident).
	var wg sync.WaitGroup
	results := make([]int, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n, err := store.PruneLocked(ctx, post, policy)
			results[i] = n
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("PruneLocked #%d: %v", i, err)
		}
	}

	totalDeleted := results[0] + results[1]
	if totalDeleted != 35 {
		t.Errorf("union of concurrent deletions: got %d want 35", totalDeleted)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_revisions WHERE post_id = $1`, post).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 5 {
		t.Errorf("survivors: got %d want 5 (MaxManual)", remaining)
	}
}

// TestPostgresStore_PruneLocked_RespectsPermanent verifies the FOR
// UPDATE SKIP LOCKED variant honours is_permanent — the SQL has
// WHERE is_permanent = FALSE so pinned rows never enter the candidate
// set.
func TestPostgresStore_PruneLocked_RespectsPermanent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skip with -short")
	}
	dsn := containers.Postgres(t)
	if dsn == "" {
		return
	}
	ctx := context.Background()
	pool := mustPool(t, dsn)
	t.Cleanup(pool.Close)
	mustInitRevisionsSchema(t, dsn)

	store := NewPostgresStore(pool)
	fixedNow := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	store.NowFunc = func() time.Time { return fixedNow }

	post := uuid.New()
	permID := uuid.New()
	// Pinned-permanent revision, old enough to otherwise be a
	// deletion candidate.
	_, err := pool.Exec(ctx, `
		INSERT INTO post_revisions
			(id, post_id, created_at, kind, snapshot, is_permanent)
		VALUES ($1, $2, $3, 'manual', '{}'::jsonb, TRUE)
	`, permID, post, fixedNow.Add(-365*24*time.Hour))
	if err != nil {
		t.Fatalf("insert permanent: %v", err)
	}

	// 10 throwaway revisions.
	for i := 0; i < 10; i++ {
		if _, ierr := pool.Exec(ctx, `
			INSERT INTO post_revisions
				(post_id, created_at, kind, snapshot)
			VALUES ($1, $2, 'manual', '{}'::jsonb)
		`, post, fixedNow.Add(time.Duration(-30*24+i)*time.Hour)); ierr != nil {
			t.Fatalf("insert: %v", ierr)
		}
	}

	_, err = store.PruneLocked(ctx, post, RetentionPolicy{MaxManual: 1})
	if err != nil {
		t.Fatalf("PruneLocked: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_revisions WHERE id = $1`, permID).Scan(&n); err != nil {
		t.Fatalf("count permanent: %v", err)
	}
	if n != 1 {
		t.Errorf("permanent revision deleted! count=%d", n)
	}
}

// mustPool opens a pgxpool. Fails the test on dial error.
func mustPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

// mustInitRevisionsSchema applies the minimal subset of the
// migration tree the pruner needs: the revision_kind enum, the
// posts/users tables it FK's against, and the post_revisions table.
//
// We don't run the full migrate tree because (a) it'd pull in a
// dozen unrelated tables and (b) the migrate package would be a
// circular import. The DDL here mirrors migrations/000001 §
// (revision_kind enum) and 000012 (post_revisions table).
func mustInitRevisionsSchema(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS pgcrypto`,
		`CREATE TYPE revision_kind AS ENUM ('autosave','manual','publish')`,
		// Minimal posts table — only the columns the FK targets.
		// We skip users entirely and make author_id nullable so the
		// FK on author_id can be left out for the test fixture.
		`CREATE TABLE posts (id UUID PRIMARY KEY)`,
		`CREATE TABLE post_revisions (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			post_id         UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
			author_id       UUID,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			kind            revision_kind NOT NULL,
			snapshot        JSONB,
			delta_from      UUID REFERENCES post_revisions(id),
			delta           JSONB,
			title           TEXT,
			excerpt         TEXT,
			content_blocks_hash BYTEA,
			comment         TEXT,
			is_permanent    BOOLEAN NOT NULL DEFAULT FALSE,
			CONSTRAINT post_revisions_snapshot_xor_delta_chk
				CHECK ((snapshot IS NOT NULL) <> (delta IS NOT NULL))
		)`,
		`CREATE INDEX post_revisions_post_created_idx ON post_revisions (post_id, created_at DESC)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("DDL %q: %v", s, err)
		}
	}

	// post_revisions has a FK to posts(id). The tests insert with
	// arbitrary UUIDs, so we insert any post id they touch first.
	// Easiest fix: drop the FK for the test schema. Equivalent
	// behaviour at the application layer; the production migration
	// keeps the FK.
	if _, err := db.ExecContext(ctx, `ALTER TABLE post_revisions DROP CONSTRAINT post_revisions_post_id_fkey`); err != nil {
		t.Fatalf("drop FK: %v", err)
	}
}
