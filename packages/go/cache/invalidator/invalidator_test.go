package invalidator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// TestWorker_DrainAndPublish exercises the happy path end-to-end:
//
//   1. Spin up Postgres (testcontainers) and Redis (miniredis).
//   2. Apply the cache_invalidations DDL.
//   3. Subscribe to the worker's channel.
//   4. Insert three rows.
//   5. Run the worker for ~3s.
//   6. Assert three pub/sub messages with the right shapes.
//   7. Assert all rows are now consumed.
func TestWorker_DrainAndPublish(t *testing.T) {
	t.Parallel()

	dsn := containers.Postgres(t)
	if dsn == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, `
		CREATE TABLE cache_invalidations (
		    id            BIGSERIAL PRIMARY KEY,
		    plugin_slug   TEXT NOT NULL,
		    tag           TEXT NOT NULL,
		    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
		    consumed_at   TIMESTAMPTZ
		);
		CREATE INDEX cache_invalidations_unconsumed_idx
		    ON cache_invalidations (id)
		    WHERE consumed_at IS NULL`); err != nil {
		t.Fatalf("create cache_invalidations: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	const testChannel = "test:invalidate"
	sub := rdb.Subscribe(ctx, testChannel)
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe ready: %v", err)
	}
	msgs := sub.Channel()

	for _, row := range []struct {
		slug, tag string
	}{
		{"gn-seo", "posts:42"},
		{"gn-seo", "sitemap"},
		{"gn-comments", "thread:1"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO cache_invalidations (plugin_slug, tag) VALUES ($1, $2)`,
			row.slug, row.tag); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	w := New(pool, rdb,
		WithChannel(testChannel),
		WithPollInterval(20*time.Millisecond))
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	var (
		runErr  error
		runDone sync.WaitGroup
	)
	runDone.Add(1)
	go func() {
		defer runDone.Done()
		runErr = w.Run(runCtx)
	}()

	got := make(map[string]bool)
	deadline := time.After(3 * time.Second)
collect:
	for len(got) < 3 {
		select {
		case <-deadline:
			break collect
		case m, ok := <-msgs:
			if !ok {
				break collect
			}
			got[m.Payload] = true
		}
	}

	runCancel()
	runDone.Wait()

	if runErr != nil {
		t.Errorf("Run: %v", runErr)
	}

	want := []string{
		"gn-seo:posts:42",
		"gn-seo:sitemap",
		"gn-comments:thread:1",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing pub/sub message %q (got %v)", w, got)
		}
	}

	var unconsumed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM cache_invalidations WHERE consumed_at IS NULL`).
		Scan(&unconsumed); err != nil {
		t.Fatalf("count unconsumed: %v", err)
	}
	if unconsumed != 0 {
		t.Errorf("unconsumed rows: got %d, want 0", unconsumed)
	}
}

// TestWorker_RejectsDoubleRun pins ErrAlreadyRunning. A second
// concurrent Run is a programming error; the worker surfaces it
// loudly rather than racing two pollers against the same rows.
func TestWorker_RejectsDoubleRun(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	dsn := "postgres://invalid@127.0.0.1:1/db?sslmode=disable"
	pool, _ := pgxpool.New(context.Background(), dsn)
	if pool == nil {
		t.Skip("no pgxpool available")
	}
	t.Cleanup(pool.Close)

	w := New(pool, rdb)

	// Pre-set the running flag so the actual Run returns
	// ErrAlreadyRunning without touching the (invalid) database.
	w.running.Store(1)
	defer w.running.Store(0)

	err := w.Run(context.Background())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Run: got %v, want ErrAlreadyRunning", err)
	}
}
