// Package invalidator drains the cache_invalidations outbox table
// and republishes each row as a Redis pub/sub notification.
//
// The outbox shape and the host-side write path live in
// packages/go/plugins/runtime/host_data.go (gn_cache_invalidate);
// migrations/000030_plugin_data_abi.up.sql owns the schema.
//
// # Why a separate worker (vs. inline notify)
//
// The host-side gn_cache_invalidate writes one row per tag into
// `cache_invalidations` INSIDE the calling plugin's transaction.
// Doing the Redis pub/sub publish inline from gn_cache_invalidate
// would break the "cache invalidation is durable" contract: a
// process crash between the Redis PUBLISH and the Postgres COMMIT
// would leave invalidations that never actually flush the cache.
// The transactional-outbox pattern fixes that by treating the
// publish as a follow-on activity driven by a poller that reads
// committed rows only.
//
// # Delivery semantics
//
// At-least-once. Each outbox row is marked consumed only after the
// PUBLISH returns successfully; a crash between PUBLISH and
// `UPDATE ... SET consumed_at` would re-publish the same
// notification on the next poll. Subscribers MUST be idempotent —
// a re-applied cache invalidation is a cheap no-op on the
// downstream cache.
//
// # Tag namespacing
//
// Tags are stored UN-prefixed in the outbox so the audit trail is
// readable ("invalidate posts:42"). The worker re-prefixes
// `<plugin_slug>:` when it publishes, so subscribers on the same
// Redis channel cannot be tricked into clearing entries that
// belong to a different plugin's namespace.
//
// # Channel layout
//
// One Redis channel: `gonext:cache:invalidate`. Each message body
// is `<plugin_slug>:<tag>` — the same shape as the Redis key
// prefix the KV ABI uses, so downstream cache layers can pattern-
// match on `plugin:<slug>:*` and react.
package invalidator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DefaultChannel is the Redis pub/sub channel the worker publishes
// invalidation messages on.
const DefaultChannel = "gonext:cache:invalidate"

// DefaultPollInterval is the time between poll cycles when there
// are no pending rows.
const DefaultPollInterval = 100 * time.Millisecond

// DefaultBatchSize is the maximum number of rows the worker drains
// per poll.
const DefaultBatchSize = 256

// Worker is the long-running cache-invalidation outbox drainer.
type Worker struct {
	pool   *pgxpool.Pool
	redis  *redis.Client
	logger *slog.Logger

	channel      string
	pollInterval time.Duration
	batchSize    int

	running atomic.Int32
}

// ErrAlreadyRunning is returned by Run if it's called concurrently
// from two goroutines.
var ErrAlreadyRunning = errors.New("invalidator: worker already running")

// Option configures a Worker at construction time.
type Option func(*Worker)

// WithLogger swaps the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(w *Worker) {
		if l != nil {
			w.logger = l
		}
	}
}

// WithChannel overrides the Redis pub/sub channel.
func WithChannel(ch string) Option {
	return func(w *Worker) {
		if ch != "" {
			w.channel = ch
		}
	}
}

// WithPollInterval overrides the idle-poll cadence.
func WithPollInterval(d time.Duration) Option {
	return func(w *Worker) {
		if d > 0 {
			w.pollInterval = d
		}
	}
}

// WithBatchSize overrides the max rows per poll cycle.
func WithBatchSize(n int) Option {
	return func(w *Worker) {
		if n > 0 {
			w.batchSize = n
		}
	}
}

// New constructs a Worker. Both pool and rdb are required;
// passing nil panics — a misconfigured Worker would silently
// drop invalidations, which is worse than a startup crash.
func New(pool *pgxpool.Pool, rdb *redis.Client, opts ...Option) *Worker {
	if pool == nil {
		panic("invalidator.New: pool is required")
	}
	if rdb == nil {
		panic("invalidator.New: redis client is required")
	}
	w := &Worker{
		pool:         pool,
		redis:        rdb,
		logger:       slog.Default(),
		channel:      DefaultChannel,
		pollInterval: DefaultPollInterval,
		batchSize:    DefaultBatchSize,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Run polls the outbox until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if !w.running.CompareAndSwap(0, 1) {
		return ErrAlreadyRunning
	}
	defer w.running.Store(0)

	w.logger.Info("cache invalidator worker started",
		slog.String("channel", w.channel),
		slog.Duration("poll_interval", w.pollInterval),
		slog.Int("batch_size", w.batchSize))

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("cache invalidator worker stopping")
			return nil
		case <-ticker.C:
		}

		drained, err := w.drainOnce(ctx)
		if err != nil {
			w.logger.Warn("cache invalidator drain error",
				slog.Any("err", err))
			continue
		}
		// Burst drain: keep going while we're hitting the batch
		// limit. Bounded by an empty queue.
		for drained == w.batchSize {
			drained, err = w.drainOnce(ctx)
			if err != nil {
				w.logger.Warn("cache invalidator drain error (burst)",
					slog.Any("err", err))
				break
			}
		}
	}
}

// drainOnce reads up to batchSize unconsumed rows, publishes each,
// and marks them consumed.
//
// Important: the tx context is decoupled from the caller's ctx. Once
// we have committed to publishing this batch, cancellation of the
// outer ctx (e.g. the worker is shutting down) must NOT strand
// already-published messages by killing the UPDATE that marks them
// consumed. Without this, a cancel between PUBLISH and UPDATE would
// leave rows that get re-published on the next worker boot — at-
// least-once is honored but pile-up under repeated restarts is
// painful. The 30s deadline keeps a wedged tx from holding locks
// forever if the outer ctx never completes naturally.
func (w *Worker) drainOnce(ctx context.Context) (int, error) {
	txCtx, txCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer txCancel()

	tx, err := w.pool.Begin(txCtx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(txCtx) }()

	rows, err := tx.Query(txCtx, `
		SELECT id, plugin_slug, tag
		FROM cache_invalidations
		WHERE consumed_at IS NULL
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`,
		w.batchSize)
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}

	type pending struct {
		id   int64
		slug string
		tag  string
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.slug, &p.tag); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan: %w", err)
		}
		batch = append(batch, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("rows: %w", err)
	}

	if len(batch) == 0 {
		_ = tx.Commit(ctx)
		return 0, nil
	}

	consumedIDs := make([]int64, 0, len(batch))
	for _, p := range batch {
		payload := fmt.Sprintf("%s:%s", p.slug, p.tag)
		if err := w.redis.Publish(txCtx, w.channel, payload).Err(); err != nil {
			w.logger.Warn("cache invalidator: publish failed",
				slog.Int64("id", p.id),
				slog.String("plugin", p.slug),
				slog.String("tag", p.tag),
				slog.Any("err", err))
			continue
		}
		consumedIDs = append(consumedIDs, p.id)
	}

	if len(consumedIDs) > 0 {
		if _, err := tx.Exec(txCtx, `
			UPDATE cache_invalidations
			SET consumed_at = now()
			WHERE id = ANY($1)`,
			consumedIDs); err != nil {
			return 0, fmt.Errorf("mark consumed: %w", err)
		}
	}

	if err := tx.Commit(txCtx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(consumedIDs), nil
}
