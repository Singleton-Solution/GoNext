package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// schemaSQL is the minimal schema the publisher/GC tests need.
// Mirrored from migrations/000001_init.up.sql + 000004_posts.up.sql
// stripped to the columns the queries actually touch. We do this
// rather than depending on the full migration loader because the
// suite intentionally only exercises the SQL the package produces.
const schemaSQL = `
CREATE TYPE post_status AS ENUM (
    'draft','pending','scheduled','published','private','trash','revision'
);
CREATE TABLE posts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_type       TEXT NOT NULL DEFAULT 'post',
    status          post_status NOT NULL DEFAULT 'draft',
    scheduled_for   TIMESTAMPTZ,
    published_at    TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    version         INTEGER NOT NULL DEFAULT 1
);
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;
CREATE TRIGGER posts_touch_updated_at
    BEFORE UPDATE ON posts
    FOR EACH ROW
    EXECUTE FUNCTION touch_updated_at();
`

// setupTestPool spins up a Postgres container with the minimal posts
// schema. Returns the pool; container cleanup is registered on t.
func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("no Postgres available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return pool
}

// TestPublishScheduled_FlipsEligibleRows covers the headline path:
// rows whose scheduled_for is in the past flip to 'published'; rows
// whose scheduled_for is in the future stay 'scheduled'.
func TestPublishScheduled_FlipsEligibleRows(t *testing.T) {
	t.Parallel()
	pool := setupTestPool(t)
	ctx := context.Background()

	now := time.Now().UTC()
	// Row A: due (past scheduled_for). Should publish.
	// Row B: still future. Should stay scheduled.
	// Row C: scheduled with no scheduled_for (defensive — shouldn't
	// be reachable through the API but the GC + UPDATE on the
	// trigger column means the column is nullable in the schema).
	// Row D: already published (should not be touched).
	for _, p := range []struct {
		status        string
		scheduledFor  *time.Time
		setPublished  bool
		alreadyPubAt  time.Time
		expectPublish bool
	}{
		{status: "scheduled", scheduledFor: ptr(now.Add(-1 * time.Minute)), expectPublish: true},
		{status: "scheduled", scheduledFor: ptr(now.Add(1 * time.Hour)), expectPublish: false},
		{status: "scheduled", scheduledFor: nil, expectPublish: false},
		{status: "published", setPublished: true, alreadyPubAt: now.Add(-24 * time.Hour)},
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO posts (status, scheduled_for, published_at)
			VALUES ($1, $2, $3)`,
			p.status, p.scheduledFor, nullableTime(p.setPublished, p.alreadyPubAt))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}
		_ = p.expectPublish
	}

	res, err := PublishScheduled(ctx, pool, PublishOptions{
		Limit: 100,
		Now:   now,
	})
	if err != nil {
		t.Fatalf("PublishScheduled: %v", err)
	}
	if res.Published != 1 {
		t.Errorf("Published: got %d, want 1", res.Published)
	}

	var publishedCount, scheduledCount int
	if err := pool.QueryRow(ctx,
		`SELECT
		    (SELECT count(*) FROM posts WHERE status='published'),
		    (SELECT count(*) FROM posts WHERE status='scheduled')`,
	).Scan(&publishedCount, &scheduledCount); err != nil {
		t.Fatalf("status counts: %v", err)
	}
	if publishedCount != 2 { // newly-flipped + the pre-existing published row
		t.Errorf("post-publish: published=%d, want 2", publishedCount)
	}
	if scheduledCount != 2 { // the future-scheduled one + the null-scheduled_for one
		t.Errorf("post-publish: scheduled=%d, want 2", scheduledCount)
	}
}

// TestPublishScheduled_PreservesPublishedAt confirms a re-publish
// keeps the original published_at value (COALESCE behaviour) so
// canonical date-URLs stay stable across re-publishes.
func TestPublishScheduled_PreservesPublishedAt(t *testing.T) {
	t.Parallel()
	pool := setupTestPool(t)
	ctx := context.Background()

	now := time.Now().UTC()
	originalPub := now.Add(-48 * time.Hour).Truncate(time.Microsecond)

	// Row that was previously published, then moved back to
	// scheduled with a new scheduled_for. published_at is retained
	// on the row.
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (status, scheduled_for, published_at)
		VALUES ('scheduled', $1, $2)`,
		now.Add(-1*time.Minute), originalPub); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := PublishScheduled(ctx, pool, PublishOptions{
		Limit: 10,
		Now:   now,
	}); err != nil {
		t.Fatalf("PublishScheduled: %v", err)
	}

	var got time.Time
	if err := pool.QueryRow(ctx,
		`SELECT published_at FROM posts WHERE status='published'`).
		Scan(&got); err != nil {
		t.Fatalf("read published_at: %v", err)
	}
	if !got.Equal(originalPub) {
		t.Errorf("published_at: got %s, want %s (original preserved)", got, originalPub)
	}
}

// TestSweepTrash_DeletesExpiredAndPreservesFresh covers the GC's
// retention boundary: rows past the cutoff are deleted, rows inside
// it are preserved.
func TestSweepTrash_DeletesExpiredAndPreservesFresh(t *testing.T) {
	t.Parallel()
	pool := setupTestPool(t)
	ctx := context.Background()

	now := time.Now().UTC()
	retention := 30 * 24 * time.Hour

	// Row A: trashed 40 days ago → delete.
	// Row B: trashed 10 days ago → keep.
	// Row C: not trashed → keep regardless.
	for _, p := range []struct {
		status    string
		updatedAt time.Time
	}{
		{status: "trash", updatedAt: now.Add(-40 * 24 * time.Hour)},
		{status: "trash", updatedAt: now.Add(-10 * 24 * time.Hour)},
		{status: "published", updatedAt: now.Add(-100 * 24 * time.Hour)},
	} {
		// Insert with the desired updated_at. The trigger will
		// fire on UPDATE but not on the initial INSERT.
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (status, updated_at) VALUES ($1, $2)`,
			p.status, p.updatedAt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	res, err := SweepTrash(ctx, pool, SweepOptions{
		Retention: retention,
		Limit:     100,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("SweepTrash: %v", err)
	}
	if res.Deleted != 1 {
		t.Errorf("Deleted: got %d, want 1", res.Deleted)
	}

	var trashed, total int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM posts WHERE status='trash'),
		    (SELECT count(*) FROM posts)`).
		Scan(&trashed, &total); err != nil {
		t.Fatalf("counts: %v", err)
	}
	if trashed != 1 {
		t.Errorf("trashed remaining: got %d, want 1", trashed)
	}
	if total != 2 {
		t.Errorf("total remaining: got %d, want 2", total)
	}
}

// TestSeedDefaults_RegistersBoth pins the wiring contract: SeedDefaults
// adds both tasks and both cron entries to the registries passed in.
func TestSeedDefaults_RegistersBoth(t *testing.T) {
	t.Parallel()
	// SeedDefaults validates the pool argument but never reads from
	// it during registration. A nil-typed but non-nil pointer would
	// be too clever; we just construct an empty pool that never
	// connects.
	pool := &pgxpool.Pool{}
	taskReg := taskspec.NewRegistry()
	cronReg := cron.NewRegistry()

	if err := SeedDefaults(taskReg, cronReg, SeedOptions{Pool: pool}); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}

	if _, ok := taskReg.Get(PublisherTaskName); !ok {
		t.Errorf("task %q not registered", PublisherTaskName)
	}
	if _, ok := taskReg.Get(GCTaskName); !ok {
		t.Errorf("task %q not registered", GCTaskName)
	}
	if _, ok := cronReg.Get(PublisherCronName); !ok {
		t.Errorf("cron %q not registered", PublisherCronName)
	}
	if _, ok := cronReg.Get(GCCronName); !ok {
		t.Errorf("cron %q not registered", GCCronName)
	}
}

// TestPublishScheduled_LimitBoundsBatch confirms the LIMIT clause
// actually caps the per-fire size. Pin against a hard-to-spot
// regression where a future refactor drops the LIMIT.
func TestPublishScheduled_LimitBoundsBatch(t *testing.T) {
	t.Parallel()
	pool := setupTestPool(t)
	ctx := context.Background()

	now := time.Now().UTC()
	past := now.Add(-1 * time.Minute)

	// Seed 5 due rows.
	for i := 0; i < 5; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (status, scheduled_for) VALUES ('scheduled', $1)`,
			past); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// Limit=2 should flip exactly two; the rest roll over.
	res, err := PublishScheduled(ctx, pool, PublishOptions{
		Limit: 2,
		Now:   now,
	})
	if err != nil {
		t.Fatalf("PublishScheduled: %v", err)
	}
	if res.Published != 2 {
		t.Errorf("Published with Limit=2: got %d, want 2", res.Published)
	}

	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM posts WHERE status='scheduled'`).
		Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 3 {
		t.Errorf("remaining scheduled: got %d, want 3", remaining)
	}
}

// ptr returns a pointer to a time value. Helper for the seed table
// in TestPublishScheduled_FlipsEligibleRows.
func ptr(t time.Time) *time.Time { return &t }

// nullableTime returns a non-nil *time.Time when ok=true, else nil.
// The pgx adapter accepts (*time.Time)(nil) as a SQL NULL.
func nullableTime(ok bool, t time.Time) *time.Time {
	if !ok {
		return nil
	}
	return &t
}
