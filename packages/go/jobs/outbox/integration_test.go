package outbox

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/dbtest"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// recordingEnqueuer satisfies Enqueuer, captures every call, and
// optionally returns a canned error for failure-path coverage.
type recordingEnqueuer struct {
	mu       sync.Mutex
	calls    []recordedCall
	errFor   map[string]error // keyed by task name; one-shot is fine for our tests
	defaultE error            // returned when errFor has no entry
}

type recordedCall struct {
	Queue   string
	Task    string
	Payload []byte
}

func (r *recordingEnqueuer) Enqueue(_ context.Context, queue, task string, payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCall{Queue: queue, Task: task, Payload: append([]byte(nil), payload...)})
	if r.errFor != nil {
		if err, ok := r.errFor[task]; ok {
			return err
		}
	}
	return r.defaultE
}

func (r *recordingEnqueuer) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingEnqueuer) callsCopy() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// fakePool is a no-op PoolQuerier used by unit tests that only need
// validate() to accept the struct. None of its methods are exercised.
type fakePool struct{}

func (fakePool) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, errors.New("fakePool: not implemented")
}
func (fakePool) Exec(context.Context, string, ...any) (pgxCommandTag, error) {
	return nil, errors.New("fakePool: not implemented")
}

// quiet returns a slog Logger that swallows everything. Cleaner than
// littering test output with the poller's INFO lines.
func quiet() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// setupOutbox starts a Postgres container, applies the minimal
// outbox DDL the tests need, and returns a ready-to-use pgxpool +
// PoolAdapter. The container is reclaimed via t.Cleanup.
//
// We don't run the project's full migrate tree because (a) it'd pull
// in a dozen unrelated tables we don't need, and (b) the migrate
// package would be a circular import. The DDL here is the SAME shape
// as migrations/000013_outbox.up.sql — keep them in sync if the
// migration ever changes.
func setupOutbox(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		// containers.Postgres already called t.Skip.
		return nil
	}

	rawDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = rawDB.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stmts := []string{
		`CREATE TABLE outbox (
			id          BIGSERIAL PRIMARY KEY,
			task_name   TEXT NOT NULL,
			payload     JSONB NOT NULL,
			queue       TEXT NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			claimed_at  TIMESTAMPTZ,
			claimed_by  TEXT,
			attempts    INT NOT NULL DEFAULT 0,
			last_error  TEXT
		)`,
		`CREATE INDEX outbox_unclaimed_idx
		    ON outbox (created_at) WHERE claimed_at IS NULL`,
		`CREATE INDEX outbox_claimed_idx
		    ON outbox (claimed_at) WHERE claimed_at IS NOT NULL`,
	}
	for _, s := range stmts {
		if _, err := rawDB.ExecContext(ctx, s); err != nil {
			t.Fatalf("DDL %q: %v", s, err)
		}
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// outboxRow is the test-helper shape we read back via direct SQL to
// assert state. Mirrors the table columns.
type outboxRow struct {
	ID         int64
	TaskName   string
	Queue      string
	Payload    []byte
	ClaimedAt  *time.Time
	ClaimedBy  *string
	Attempts   int
	LastError  *string
}

func readAll(t *testing.T, pool *pgxpool.Pool) []outboxRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := pool.Query(ctx, `SELECT id, task_name, queue, payload, claimed_at, claimed_by, attempts, last_error
		FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.TaskName, &r.Queue, &r.Payload, &r.ClaimedAt, &r.ClaimedBy, &r.Attempts, &r.LastError); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	return out
}

// TestIntegration_Store_RollbackDropsRow exercises the core
// transactional contract: a Write inside a tx that rolls back leaves
// no trace.
//
// This is also a worked example of dbtest.BeginIsolated: instead of
// hand-rolling Begin + Rollback (and a defer to handle the failure
// path), we hand the test's tx to a helper that wires the rollback
// into t.Cleanup. The test body then has only the assertion logic.
// Note that store.Write runs against the tx returned by BeginIsolated
// — it's a real pgx.Tx — and the readAll() probe against the bare
// pool sees zero rows because the tx is rolled back at cleanup,
// before this function returns? No — readAll runs INSIDE the test
// body, so the tx is still open at that point. Postgres's per-tx
// isolation gives us the same observable property: uncommitted
// writes are invisible to other connections.
func TestIntegration_Store_RollbackDropsRow(t *testing.T) {
	pool := setupOutbox(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	store := NewStore()

	tx := dbtest.BeginIsolated(t, pool)
	if _, err := store.Write(ctx, tx, Entry{
		TaskName: "x.rollback", Queue: "q", Payload: map[string]any{"a": 1},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The tx is still open here — readAll opens a separate connection
	// off the pool, which can't see uncommitted writes. After this
	// function returns, t.Cleanup → Rollback discards the row, so a
	// hypothetical second test reusing this pool would also see zero
	// rows. Either way the contract holds.
	if rows := readAll(t, pool); len(rows) != 0 {
		t.Fatalf("expected 0 rows visible from outside the tx, got %d: %+v", len(rows), rows)
	}
}

// TestIntegration_Store_CommitPersistsRow + Poller drains it.
func TestIntegration_StoreCommitThenPollerDrains(t *testing.T) {
	pool := setupOutbox(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	store := NewStore()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := store.Write(ctx, tx, Entry{
		TaskName: "email.send", Queue: "default",
		Payload: map[string]any{"to": "alice@example.com"},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	rows := readAll(t, pool)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after commit, got %d", len(rows))
	}
	if rows[0].TaskName != "email.send" {
		t.Errorf("task_name: got %q want email.send", rows[0].TaskName)
	}

	enq := &recordingEnqueuer{}
	p := &Poller{
		Pool:     NewPoolAdapter(pool),
		Enqueuer: enq,
		WorkerID: "w1",
		Logger:   quiet(),
	}
	handled, err := p.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if handled != 1 {
		t.Errorf("handled: got %d want 1", handled)
	}
	if enq.callCount() != 1 {
		t.Errorf("enqueuer calls: got %d want 1", enq.callCount())
	}
	call := enq.callsCopy()[0]
	if call.Queue != "default" || call.Task != "email.send" {
		t.Errorf("call shape: got %+v", call)
	}
	if rows := readAll(t, pool); len(rows) != 0 {
		t.Errorf("row should be deleted after enqueue, got %+v", rows)
	}
	if got := p.Drained(); got != 1 {
		t.Errorf("Drained: got %d want 1", got)
	}
}

// TestIntegration_TwoPollers_NoDoubleClaim seeds N rows, runs two
// pollers concurrently, and asserts that across both pollers every
// row was enqueued exactly once (no double-claim, no row dropped).
func TestIntegration_TwoPollers_NoDoubleClaim(t *testing.T) {
	pool := setupOutbox(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	store := NewStore()
	const n = 80

	// Seed.
	for i := 0; i < n; i++ {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin %d: %v", i, err)
		}
		if _, err := store.Write(ctx, tx, Entry{
			TaskName: "concurrent.task", Queue: "q",
			Payload: map[string]any{"i": i},
		}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("Commit %d: %v", i, err)
		}
	}

	enq := &recordingEnqueuer{}
	makePoller := func(id string) *Poller {
		return &Poller{
			Pool:      NewPoolAdapter(pool),
			Enqueuer:  enq,
			WorkerID:  id,
			BatchSize: 8, // small so the two pollers genuinely interleave
			Logger:    quiet(),
		}
	}
	p1 := makePoller("w1")
	p2 := makePoller("w2")

	// Run both pollers in parallel until the table is empty. We cap
	// the loop count so a bug doesn't hang the test forever.
	var wg sync.WaitGroup
	done := make(chan struct{})
	run := func(p *Poller) {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			handled, err := p.RunOnce(ctx)
			if err != nil {
				t.Errorf("RunOnce on %s: %v", p.WorkerID, err)
				return
			}
			if handled == 0 {
				// Brief sleep to let the other poller make progress
				// (avoids burning CPU spinning).
				time.Sleep(2 * time.Millisecond)
			}
		}
	}
	wg.Add(2)
	go run(p1)
	go run(p2)

	// Wait until everything's drained or we time out.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if enq.callCount() >= n && len(readAll(t, pool)) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(done)
	wg.Wait()

	if got := enq.callCount(); got != n {
		t.Errorf("enqueue calls: got %d want %d", got, n)
	}
	// No row should remain.
	if remaining := readAll(t, pool); len(remaining) != 0 {
		t.Errorf("rows left: %d", len(remaining))
	}
	// Per-row uniqueness: every seeded i must appear exactly once.
	seen := make(map[string]bool, n)
	for _, c := range enq.callsCopy() {
		key := string(c.Payload)
		if seen[key] {
			t.Errorf("duplicate enqueue of payload %s", key)
		}
		seen[key] = true
	}
	if len(seen) != n {
		t.Errorf("unique enqueues: got %d want %d", len(seen), n)
	}
}

// TestIntegration_Recoverer_ReleasesStuckRows seeds a row, hand-
// claims it on behalf of a dead worker (so the lease is set far in
// the past), and verifies the recoverer releases it on the next
// sweep. A fresh poller then picks the row up.
func TestIntegration_Recoverer_ReleasesStuckRows(t *testing.T) {
	pool := setupOutbox(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	store := NewStore()

	// Seed.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := store.Write(ctx, tx, Entry{
		TaskName: "stuck", Queue: "q", Payload: map[string]any{"v": 1},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Manually mark the row as claimed by a "dead" worker, 10 minutes
	// ago. This is exactly what claim() would have done if the
	// poller had crashed before deleting.
	staleClaim := time.Now().Add(-10 * time.Minute).UTC()
	if _, err := pool.Exec(ctx, `UPDATE outbox SET claimed_at = $1, claimed_by = $2`,
		staleClaim, "dead-worker"); err != nil {
		t.Fatalf("manual claim: %v", err)
	}

	// Poller with a fresh worker id sees an empty unclaimed set.
	enq := &recordingEnqueuer{}
	p := &Poller{
		Pool: NewPoolAdapter(pool), Enqueuer: enq,
		WorkerID: "live", Logger: quiet(),
	}
	handled, err := p.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce pre-sweep: %v", err)
	}
	if handled != 0 || enq.callCount() != 0 {
		t.Errorf("poller should not claim stuck row before sweep: handled=%d, calls=%d", handled, enq.callCount())
	}

	// Run the recoverer with a 60-second lease. The 10-minute-old
	// claim is far past expiry.
	rec := &Recoverer{
		Pool:          NewPoolAdapter(pool),
		ClaimLeaseSec: 60,
		Logger:        quiet(),
	}
	released, err := rec.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if released != 1 {
		t.Errorf("released: got %d want 1", released)
	}

	// Now the live poller picks it up.
	handled, err = p.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce post-sweep: %v", err)
	}
	if handled != 1 {
		t.Errorf("handled post-sweep: got %d want 1", handled)
	}
	if enq.callCount() != 1 {
		t.Errorf("enqueue calls: got %d want 1", enq.callCount())
	}
}

// TestIntegration_EnqueueError_RowStaysAndAttemptsBumped seeds a row,
// runs the poller with an Enqueuer that always fails, and asserts:
//
//  1. The row is NOT deleted (the data lives on).
//  2. attempts is incremented.
//  3. last_error is captured.
//  4. claimed_at is now in the future (backoff active) — meaning a
//     repeat poll cycle skips the row until the backoff elapses.
func TestIntegration_EnqueueError_RowStaysAndAttemptsBumped(t *testing.T) {
	pool := setupOutbox(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	store := NewStore()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := store.Write(ctx, tx, Entry{
		TaskName: "will.fail", Queue: "q",
		Payload: map[string]any{"x": 1},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	bang := errors.New("redis is down")
	enq := &recordingEnqueuer{defaultE: bang}
	p := &Poller{
		Pool: NewPoolAdapter(pool), Enqueuer: enq,
		WorkerID:   "w1",
		BackoffMin: 5 * time.Second,
		BackoffMax: 30 * time.Second,
		Logger:     quiet(),
	}
	handled, err := p.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// "handled" is the count of successful drains, not attempts.
	if handled != 0 {
		t.Errorf("handled: got %d want 0", handled)
	}

	rows := readAll(t, pool)
	if len(rows) != 1 {
		t.Fatalf("row should still exist after enqueue error, got %d rows", len(rows))
	}
	r := rows[0]
	if r.Attempts != 1 {
		t.Errorf("attempts: got %d want 1", r.Attempts)
	}
	if r.LastError == nil || *r.LastError != "redis is down" {
		t.Errorf("last_error: got %v want %q", r.LastError, "redis is down")
	}
	if r.ClaimedAt == nil {
		t.Fatal("claimed_at should be set (backoff is active)")
	}
	// The backoff for prevAttempts=0 is BackoffMin (5s). Allow a 1s
	// tolerance for clock variance / round-trip jitter.
	wantMin := time.Now().Add(3 * time.Second)
	if r.ClaimedAt.Before(wantMin) {
		t.Errorf("claimed_at should be ~5s in the future, got %v (now=%v)", *r.ClaimedAt, time.Now())
	}

	// A second poll cycle right now should NOT pick the row up — the
	// backoff window covers it. claim() filters on
	// `claimed_at IS NULL`, so a future timestamp means "in flight".
	if h, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 2: %v", err)
	} else if h != 0 {
		t.Errorf("second poll should respect backoff, handled=%d", h)
	}
	if enq.callCount() != 1 {
		t.Errorf("enqueuer should not have been called twice yet, got %d", enq.callCount())
	}
}

// TestIntegration_BackoffRespected_AfterRecover seeds a row, fails
// the enqueue (which pushes claimed_at into the future), then runs
// the recoverer with the clock advanced past both the backoff and
// the lease — the recoverer releases the row, and a new poll cycle
// picks it up.
func TestIntegration_BackoffRespected_AfterRecover(t *testing.T) {
	pool := setupOutbox(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	store := NewStore()

	tx, _ := pool.Begin(ctx)
	if _, err := store.Write(ctx, tx, Entry{
		TaskName: "transient.fail", Queue: "q", Payload: map[string]any{"x": 1},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = tx.Commit(ctx)

	// Pin time so we can step the clock deterministically.
	t0 := time.Now()
	clock := atomic.Pointer[time.Time]{}
	clock.Store(&t0)
	getNow := func() time.Time { return *clock.Load() }
	setNow := func(t time.Time) { clock.Store(&t) }

	// Enqueuer fails the first call, succeeds afterwards.
	var firstCall atomic.Bool
	firstCall.Store(true)
	enq := EnqueueFunc(func(_ context.Context, _, _ string, _ []byte) error {
		if firstCall.CompareAndSwap(true, false) {
			return errors.New("flake")
		}
		return nil
	})

	p := &Poller{
		Pool:       NewPoolAdapter(pool),
		Enqueuer:   enq,
		WorkerID:   "w",
		BackoffMin: 2 * time.Second,
		BackoffMax: 10 * time.Second,
		NowFunc:    getNow,
		Logger:     quiet(),
	}

	// First cycle: fails, row gets backoff'd.
	if h, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("cycle 1: %v", err)
	} else if h != 0 {
		t.Errorf("cycle 1 handled: got %d want 0", h)
	}

	rows := readAll(t, pool)
	if len(rows) != 1 {
		t.Fatalf("row should still exist, got %d", len(rows))
	}
	if rows[0].Attempts != 1 {
		t.Errorf("attempts: got %d want 1", rows[0].Attempts)
	}

	// Advance clock past the backoff window AND past the lease.
	// Recoverer with 1s lease will release.
	setNow(t0.Add(20 * time.Second))
	rec := &Recoverer{
		Pool:          NewPoolAdapter(pool),
		ClaimLeaseSec: 1,
		NowFunc:       getNow,
		Logger:        quiet(),
	}
	released, err := rec.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if released != 1 {
		t.Errorf("released: got %d want 1", released)
	}

	// Second poll cycle now sees an unclaimed row → enqueues
	// successfully (the second call returns nil) → deletes.
	if h, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("cycle 2: %v", err)
	} else if h != 1 {
		t.Errorf("cycle 2 handled: got %d want 1", h)
	}
	if rows := readAll(t, pool); len(rows) != 0 {
		t.Errorf("row should be deleted after successful retry, got %+v", rows)
	}
}

// TestIntegration_Poller_RunUntilContextCancel runs the live Run
// loop, asserts it drains rows that arrive during the loop, and
// shuts down cleanly on context cancel.
func TestIntegration_Poller_RunUntilContextCancel(t *testing.T) {
	pool := setupOutbox(t)
	if pool == nil {
		return
	}
	store := NewStore()

	// Seed one row up front, then one more after the loop has been
	// spinning for a moment.
	mustInsert := func(payload string) {
		ctx := context.Background()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := store.Write(ctx, tx, Entry{
			TaskName: "rl.task", Queue: "q",
			Payload: map[string]any{"p": payload},
		}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	mustInsert("first")

	enq := &recordingEnqueuer{}
	p := &Poller{
		Pool:         NewPoolAdapter(pool),
		Enqueuer:     enq,
		WorkerID:     "loop",
		PollInterval: 10 * time.Millisecond, // fast, so the test runs quickly
		Logger:       quiet(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	// Wait until the first row is drained, then insert a second.
	waitFor := func(want int, deadline time.Duration) {
		end := time.Now().Add(deadline)
		for time.Now().Before(end) {
			if enq.callCount() >= want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d enqueues, got %d", want, enq.callCount())
	}
	waitFor(1, 5*time.Second)
	mustInsert("second")
	waitFor(2, 5*time.Second)

	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", err)
	}
	if remaining := readAll(t, pool); len(remaining) != 0 {
		t.Errorf("rows left after drain: %d", len(remaining))
	}
}

// EnqueueFunc adapts a function to the Enqueuer interface — handy in
// tests that want bespoke per-call behaviour.
type EnqueueFunc func(ctx context.Context, queue, task string, payload []byte) error

func (f EnqueueFunc) Enqueue(ctx context.Context, queue, task string, payload []byte) error {
	return f(ctx, queue, task, payload)
}

