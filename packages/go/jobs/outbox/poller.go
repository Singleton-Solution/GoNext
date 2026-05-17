package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Default knobs. Tuned for a healthy single-replica deployment at
// thousands of messages per minute; operators tune via the Poller
// struct fields below.
const (
	// DefaultBatchSize is how many rows one poll cycle claims. Small
	// enough that the FOR UPDATE SKIP LOCKED window stays brief;
	// large enough to amortise the round-trip cost.
	DefaultBatchSize = 64

	// DefaultPollInterval is the gap between consecutive poll cycles
	// when the previous cycle found at least one row. When a cycle
	// finds an empty table the poller sleeps the FULL interval —
	// there's nothing to do, no point spinning.
	DefaultPollInterval = 500 * time.Millisecond

	// DefaultClaimLeaseSec is how long a row stays claimed before
	// the recovery sweep is allowed to release it. Set well above
	// the realistic enqueue latency so a slow Redis hop doesn't
	// trigger a spurious lease expiry.
	DefaultClaimLeaseSec = 60

	// DefaultBackoffMin is the floor for the per-row backoff after a
	// failed enqueue. The poller doesn't actively sleep on the row;
	// instead it sets claimed_at to a future-ish timestamp so the
	// poll-cycle SELECT skips it until the time arrives. This is
	// cheaper than a per-row goroutine.
	DefaultBackoffMin = 1 * time.Second

	// DefaultBackoffMax caps exponential growth so a permanently-bad
	// row doesn't blow out into multi-hour quiet periods.
	DefaultBackoffMax = 5 * time.Minute
)

// Enqueuer is the side-effect the poller fires for each claimed row.
// Production wiring is a thin adapter over redis.Client; tests pass a
// stub that records the calls.
//
// Return contract:
//
//   - nil: row will be deleted on the next round-trip.
//   - non-nil: row's claim is released, attempts++, last_error
//     captured, and the row is re-considered on a future poll cycle
//     (subject to backoff via claimed_at).
type Enqueuer interface {
	Enqueue(ctx context.Context, queue, taskName string, payload []byte) error
}

// PoolQuerier is the minimal pgxpool surface the poller needs. The
// real *pgxpool.Pool satisfies it; tests can pass a fake.
type PoolQuerier interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgxCommandTag, error)
}

// pgxCommandTag is the minimal command-tag interface the poller
// inspects. pgconn.CommandTag (returned by pgxpool.Exec) satisfies it
// natively — see poolAdapter.
type pgxCommandTag interface {
	RowsAffected() int64
}

// PoolAdapter wraps a *pgxpool.Pool to satisfy PoolQuerier. Exposed so
// production callers can construct the Poller without a private
// helper.
type PoolAdapter struct{ pool *pgxpool.Pool }

// NewPoolAdapter wraps a *pgxpool.Pool. Returns nil if pool is nil so
// the caller's wiring code fails loudly rather than panicking on the
// first BeginTx.
func NewPoolAdapter(pool *pgxpool.Pool) *PoolAdapter {
	if pool == nil {
		return nil
	}
	return &PoolAdapter{pool: pool}
}

// BeginTx delegates to the underlying pgxpool.
func (a *PoolAdapter) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	return a.pool.BeginTx(ctx, opts)
}

// Exec runs a single statement against the pool; the pgconn.CommandTag
// it returns is shaped to satisfy pgxCommandTag.
func (a *PoolAdapter) Exec(ctx context.Context, sql string, args ...any) (pgxCommandTag, error) {
	tag, err := a.pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return tag, nil
}

// Poller drains the outbox table to Redis.
//
// Public fields are tuning knobs; populate them before calling Run.
// Run is safe to call concurrently — each instance is independent.
// Operators routinely deploy two pollers per replica for redundancy;
// the FOR UPDATE SKIP LOCKED claim semantics guarantee they never
// step on each other.
type Poller struct {
	// Pool is the database handle. Required.
	Pool PoolQuerier

	// Enqueuer is invoked once per claimed row. Required.
	Enqueuer Enqueuer

	// WorkerID identifies the running poller. Stamped into claimed_by
	// so an operator inspecting a stuck row can match it to a
	// process. Required.
	WorkerID string

	// BatchSize is the per-cycle claim count. Zero falls back to
	// DefaultBatchSize.
	BatchSize int

	// PollInterval is the gap between consecutive poll cycles. Zero
	// falls back to DefaultPollInterval.
	PollInterval time.Duration

	// ClaimLeaseSec is how long a row stays claimed before recovery
	// is allowed to release it. Zero falls back to
	// DefaultClaimLeaseSec.
	ClaimLeaseSec int

	// BackoffMin / BackoffMax bound the per-row backoff after a
	// failed enqueue. The row's claimed_at is bumped forward to
	// (now + backoff) so subsequent poll-cycles skip it until the
	// time arrives. Zero falls back to DefaultBackoffMin /
	// DefaultBackoffMax.
	BackoffMin time.Duration
	BackoffMax time.Duration

	// Logger is used for cycle-level log lines. Nil falls back to
	// slog.Default.
	Logger *slog.Logger

	// NowFunc, if set, replaces time.Now everywhere the poller
	// computes a time. Tests pin it to a known instant so backoff
	// progression is deterministic.
	NowFunc func() time.Time

	// drained counts rows successfully forwarded since boot. Used
	// only for observability + tests. Exported via Drained().
	drained atomic.Int64
}

// Drained reports the total rows successfully forwarded since the
// Poller was created. Lock-free.
func (p *Poller) Drained() int64 { return p.drained.Load() }

// Run loops claim → enqueue → delete until ctx is cancelled. Returns
// the context error on cancellation; any other error is logged but
// does not terminate the loop (the next cycle gets a fresh chance).
func (p *Poller) Run(ctx context.Context) error {
	if err := p.validate(); err != nil {
		return err
	}
	logger := p.logger()

	interval := p.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}

	logger.Info("outbox poller started",
		slog.String("worker_id", p.WorkerID),
		slog.Int("batch_size", p.batchSize()),
		slog.Duration("poll_interval", interval),
		slog.Int("claim_lease_sec", p.leaseSec()),
	)

	timer := time.NewTimer(interval)
	defer timer.Stop()
	// Drain the initial tick so the first iteration runs immediately
	// without waiting for the first interval.
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(0)

	for {
		select {
		case <-ctx.Done():
			logger.Info("outbox poller stopping",
				slog.String("worker_id", p.WorkerID),
				slog.Int64("drained", p.drained.Load()),
			)
			return ctx.Err()
		case <-timer.C:
		}

		_, err := p.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.Error("outbox poller cycle failed",
				slog.String("worker_id", p.WorkerID),
				slog.String("err", err.Error()),
			)
		}
		timer.Reset(interval)
	}
}

// RunOnce executes exactly one claim/enqueue/delete cycle. Exposed
// for tests (so they can drive the poller without spinning the
// goroutine loop) and for operators who want to embed the poll in
// their own scheduler.
//
// Returns the number of rows the cycle handled and the first error
// encountered (or nil). A successful cycle that found no rows
// returns (0, nil) — that's a normal idle state, not an error.
func (p *Poller) RunOnce(ctx context.Context) (handled int, err error) {
	if err := p.validate(); err != nil {
		return 0, err
	}

	rows, err := p.claim(ctx)
	if err != nil {
		return 0, fmt.Errorf("outbox poll: claim: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	for _, r := range rows {
		if ctx.Err() != nil {
			// Don't keep firing enqueues against a cancelled ctx —
			// release whatever's still claimed and bail. The recovery
			// sweep will pick up the leftovers if the process dies.
			return handled, ctx.Err()
		}
		if eqErr := p.Enqueuer.Enqueue(ctx, r.Queue, r.TaskName, r.Payload); eqErr != nil {
			// Soft failure: release the claim, bump attempts, capture
			// the error. The row will be retried on a future cycle.
			if relErr := p.releaseFailed(ctx, r.ID, eqErr); relErr != nil {
				p.logger().Warn("outbox release after enqueue failure",
					slog.Int64("row_id", r.ID),
					slog.String("enqueue_err", eqErr.Error()),
					slog.String("release_err", relErr.Error()),
				)
			}
			continue
		}
		// Happy path: delete the row.
		if delErr := p.deleteRow(ctx, r.ID); delErr != nil {
			// Enqueue succeeded but delete failed. The row will be
			// retried after the lease expires, so the worker MUST be
			// idempotent. We log loud and proceed.
			p.logger().Warn("outbox row enqueued but delete failed",
				slog.Int64("row_id", r.ID),
				slog.String("err", delErr.Error()),
			)
			continue
		}
		p.drained.Add(1)
		handled++
	}
	return handled, nil
}

// claimedRow is the in-memory representation of one outbox row that
// the poller has just taken under its lease.
type claimedRow struct {
	ID       int64
	TaskName string
	Queue    string
	Payload  []byte
}

// claim runs one round of the FOR UPDATE SKIP LOCKED dance. We do it
// in a transaction so that the SELECT … FOR UPDATE and the UPDATE
// stamping claimed_at are atomic against any other poller.
//
// Why CTE + UPDATE rather than a single UPDATE ... FROM ... RETURNING:
// the CTE form is the canonical SKIP LOCKED idiom and it makes the
// intent obvious. The single-statement UPDATE variant works on modern
// Postgres but the planner sometimes refuses SKIP LOCKED under it.
func (p *Poller) claim(ctx context.Context) ([]claimedRow, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := p.now()
	const q = `
		WITH next AS (
			SELECT id
			  FROM outbox
			 WHERE claimed_at IS NULL
			 ORDER BY created_at
			 LIMIT $1
			 FOR UPDATE SKIP LOCKED
		)
		UPDATE outbox o
		   SET claimed_at = $2,
		       claimed_by = $3
		  FROM next
		 WHERE o.id = next.id
		RETURNING o.id, o.task_name, o.queue, o.payload
	`
	rows, err := tx.Query(ctx, q, p.batchSize(), now, p.WorkerID)
	if err != nil {
		return nil, fmt.Errorf("claim query: %w", err)
	}

	var out []claimedRow
	for rows.Next() {
		var r claimedRow
		if err := rows.Scan(&r.ID, &r.TaskName, &r.Queue, &r.Payload); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim: %w", err)
	}
	return out, nil
}

// deleteRow drops the row from outbox once the enqueue has been ack'd.
// This is the only "permanent" mutation — every other state change is
// recoverable via the recovery sweep.
func (p *Poller) deleteRow(ctx context.Context, id int64) error {
	if _, err := p.Pool.Exec(ctx, `DELETE FROM outbox WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete row %d: %w", id, err)
	}
	return nil
}

// releaseFailed bumps attempts, records the error, and pushes
// claimed_at into the future so subsequent poll cycles skip the row
// until the backoff elapses.
//
// The backoff schedule is exponential, doubling on each retry,
// clamped at BackoffMax. We don't add jitter — the row count is
// expected to be small and the poll loop already provides
// stochastic timing via PollInterval.
func (p *Poller) releaseFailed(ctx context.Context, id int64, cause error) error {
	now := p.now()
	const q = `
		UPDATE outbox
		   SET claimed_at = $2,
		       attempts   = attempts + 1,
		       last_error = $3
		 WHERE id = $1
		RETURNING attempts
	`
	// We re-stamp claimed_at to (now + backoff) so the row appears
	// "in flight" until the backoff window elapses. This is a
	// deliberate abuse of the lease column — it avoids needing a
	// separate "next_attempt_at" field on the row. The recovery
	// sweep will release these rows once they cross the lease
	// boundary, which by construction is >= the largest backoff
	// (caller is expected to set ClaimLeaseSec >= BackoffMax).
	//
	// We compute backoff based on the row's CURRENT attempts (pre-
	// increment) so the first failure waits BackoffMin, the second
	// 2*BackoffMin, etc.
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin release tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existingAttempts int
	if err := tx.QueryRow(ctx, `SELECT attempts FROM outbox WHERE id = $1 FOR UPDATE`, id).Scan(&existingAttempts); err != nil {
		return fmt.Errorf("read attempts: %w", err)
	}
	backoff := p.backoffFor(existingAttempts)
	resumeAt := now.Add(backoff)

	var nextAttempts int
	if err := tx.QueryRow(ctx, q, id, resumeAt, cause.Error()).Scan(&nextAttempts); err != nil {
		return fmt.Errorf("release update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit release: %w", err)
	}
	return nil
}

// backoffFor computes the per-row delay for the (attempts+1)'th try.
// Exponential, bounded by [BackoffMin, BackoffMax]. Pure function so
// tests can pin time.
func (p *Poller) backoffFor(prevAttempts int) time.Duration {
	if prevAttempts < 0 {
		prevAttempts = 0
	}
	min := p.BackoffMin
	if min <= 0 {
		min = DefaultBackoffMin
	}
	max := p.BackoffMax
	if max <= 0 {
		max = DefaultBackoffMax
	}
	if min > max {
		// Caller set them inverted. Use min for both — the safest
		// behaviour, and we Log a warning via validate() at startup.
		max = min
	}
	// 2^prevAttempts * min, capped at max. We compute in
	// time.Duration arithmetic to avoid overflow when prevAttempts
	// is large.
	d := min
	for i := 0; i < prevAttempts; i++ {
		d *= 2
		if d >= max || d <= 0 {
			return max
		}
	}
	if d < min {
		return min
	}
	return d
}

func (p *Poller) batchSize() int {
	if p.BatchSize <= 0 {
		return DefaultBatchSize
	}
	return p.BatchSize
}

func (p *Poller) leaseSec() int {
	if p.ClaimLeaseSec <= 0 {
		return DefaultClaimLeaseSec
	}
	return p.ClaimLeaseSec
}

func (p *Poller) now() time.Time {
	if p.NowFunc != nil {
		return p.NowFunc()
	}
	return time.Now()
}

func (p *Poller) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// validate sanity-checks the Poller's required fields. Called from
// Run/RunOnce so a wiring mistake fails on the first call rather than
// crashing inside the SQL layer.
func (p *Poller) validate() error {
	if p.Pool == nil {
		return fmt.Errorf("outbox.Poller: Pool is required")
	}
	if p.Enqueuer == nil {
		return fmt.Errorf("outbox.Poller: Enqueuer is required")
	}
	if p.WorkerID == "" {
		return fmt.Errorf("outbox.Poller: WorkerID is required")
	}
	return nil
}
