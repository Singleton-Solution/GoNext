package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

// PublisherTaskName is the on-wire task name the cron entry fires
// against. Exported so the worker wiring and the admin "publish now"
// surface can target the same handler.
const PublisherTaskName = "content.publisher"

// PublisherCronName is the cron-registry key for the per-minute fire.
const PublisherCronName = "content.publisher.minute"

// PublisherSchedule is the cron expression that fires the publisher.
// "@every 1m" matches the smallest cadence the project's cron parser
// accepts and is the resolution the editor surface advertises in the
// "schedule for…" picker.
const PublisherSchedule = "@every 1m"

// DefaultPublisherBatchLimit caps how many posts a single fire flips
// to 'published'. The cap protects against a pathological backlog
// (e.g. a frozen worker accumulating thousands of scheduled posts)
// pinning the database under a single transaction.
const DefaultPublisherBatchLimit = 500

// publisherPayloadSchemaRaw constrains the task payload. The task
// takes no per-fire input — Cron will pass `null` and we validate
// against that.
var publisherPayloadSchemaRaw = []byte(`{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type": ["object", "null"],
	"additionalProperties": false
}`)

// PublisherResult is the per-run summary surfaced to logs and
// (eventually) Prometheus. We expose it as a named type so tests can
// assert on the shape; in production the worker just logs it.
type PublisherResult struct {
	// Scanned is the number of rows the candidate query returned.
	// Equal to Published in the steady state; lower than Published
	// would be a bug.
	Scanned int

	// Published is the number of rows actually flipped to
	// 'published'. May be less than Scanned if a row was modified
	// (version bump) between the candidate query and the UPDATE —
	// the trigger-driven version check filters out concurrent edits.
	Published int

	// Errors is the number of rows the publisher attempted to flip
	// but couldn't (typically a constraint violation, e.g. a
	// unique-slug collision after a slug template change). Each
	// errored row is logged individually.
	Errors int
}

// PublisherSpecOptions configures NewPublisherSpec. Pool is required
// — the task handler is a closure over it. The rest tune behaviour.
type PublisherSpecOptions struct {
	// Pool is the pgx connection pool. Required.
	Pool *pgxpool.Pool

	// Limit is the maximum number of rows one fire touches.
	// Defaults to DefaultPublisherBatchLimit.
	Limit int

	// Logger receives the structured per-fire output. Nil falls
	// back to slog.Default.
	Logger *slog.Logger

	// Now is a clock override for tests. Nil defaults to time.Now.
	Now func() time.Time
}

// NewPublisherSpec returns the TaskSpec the worker registers for the
// scheduled-publisher task. The handler closes over opts.Pool so the
// cron side does not need to know the database configuration.
func NewPublisherSpec(opts PublisherSpecOptions) (taskspec.TaskSpec, error) {
	if opts.Pool == nil {
		return taskspec.TaskSpec{}, errors.New("scheduler: NewPublisherSpec: Pool is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultPublisherBatchLimit
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	handler := func(ctx context.Context, raw []byte) error {
		// Parse the payload defensively. We don't read any keys
		// from it, but a stray non-null payload should surface as
		// a parse error rather than be silently ignored — that's
		// the same shape every other task in the project uses.
		if len(raw) > 0 && string(raw) != "null" {
			var anyPayload map[string]any
			if err := json.Unmarshal(raw, &anyPayload); err != nil {
				return fmt.Errorf("scheduler/publisher: parse payload: %w", err)
			}
		}
		res, err := PublishScheduled(ctx, opts.Pool, PublishOptions{
			Limit:  opts.Limit,
			Now:    opts.Now(),
			Logger: opts.Logger,
		})
		if err != nil {
			return fmt.Errorf("scheduler/publisher: %w", err)
		}
		opts.Logger.InfoContext(ctx, "scheduler/publisher: fire complete",
			slog.Int("scanned", res.Scanned),
			slog.Int("published", res.Published),
			slog.Int("errors", res.Errors))
		return nil
	}
	return taskspec.TaskSpec{
		Name:     PublisherTaskName,
		Queue:    "default",
		MaxRetry: 1,
		// 2 minutes covers a full-batch sweep with comfortable
		// headroom for an over-loaded Postgres. The next fire
		// arrives in 60s, so timing out earlier would mean we'd
		// double-fire while the previous run is still alive.
		Timeout: 2 * time.Minute,
		Handler: handler,
	}, nil
}

// NewPublisherCron returns the CronSpec registered against the cron
// registry. "@every 1m" payload nil.
func NewPublisherCron() cron.CronSpec {
	return cron.CronSpec{
		Name:     PublisherCronName,
		Schedule: PublisherSchedule,
		TaskName: PublisherTaskName,
	}
}

// PublisherPayloadSchema returns the JSON Schema bytes. Exposed so
// tests can validate payloads outside the Enqueue path.
func PublisherPayloadSchema() []byte {
	out := make([]byte, len(publisherPayloadSchemaRaw))
	copy(out, publisherPayloadSchemaRaw)
	return out
}

// PublishOptions tunes one PublishScheduled invocation. Production
// callers pass nothing; tests pass a fixed Now to make the candidate
// query deterministic.
type PublishOptions struct {
	// Limit caps the number of rows touched per call. Required to
	// be positive; PublishScheduled returns an error on zero.
	Limit int

	// Now is the wall-clock time used for the scheduled_for <=
	// comparison and the published_at value. Required.
	Now time.Time

	// Logger receives structured per-row warnings. Nil falls back
	// to slog.Default.
	Logger *slog.Logger
}

// PublishScheduled is the pure transition step exposed without the
// taskspec wrapper. It's the function the handler calls and the
// one tests target directly — much easier to assert against than a
// json.RawMessage-shaped handler.
//
// Behaviour:
//
//	UPDATE posts
//	SET status='published', published_at=COALESCE(published_at, $1)
//	WHERE status='scheduled' AND scheduled_for <= $1
//	RETURNING id
//
// The COALESCE on published_at is what the schema comment promises —
// a draft of a previously-published post keeps its original
// publication date so canonical URLs stay stable.
//
// The update is bounded by $LIMIT to keep one fire from holding
// locks longer than the cron interval. Excess rows roll over to the
// next minute's fire.
//
// Errors here propagate from pgx — a missing posts table, a typo'd
// status enum, a constraint trip on a unique slug. The handler
// retries once via asynq, then DLQs.
func PublishScheduled(ctx context.Context, pool *pgxpool.Pool, opts PublishOptions) (PublisherResult, error) {
	if pool == nil {
		return PublisherResult{}, errors.New("scheduler: PublishScheduled: pool is required")
	}
	if opts.Limit <= 0 {
		return PublisherResult{}, errors.New("scheduler: PublishScheduled: Limit must be positive")
	}
	if opts.Now.IsZero() {
		return PublisherResult{}, errors.New("scheduler: PublishScheduled: Now is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// One UPDATE … RETURNING id. The CTE shape (with LIMIT on a
	// SELECT … FOR UPDATE SKIP LOCKED, then UPDATE on the locked
	// set) is the canonical "drain a queue safely" pattern: if two
	// workers somehow race on the same fire, each takes a disjoint
	// slice and neither errors.
	rows, err := pool.Query(ctx, `
		WITH due AS (
			SELECT id
			FROM posts
			WHERE status = 'scheduled'
			  AND scheduled_for IS NOT NULL
			  AND scheduled_for <= $1
			ORDER BY scheduled_for
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE posts p
		SET status       = 'published',
		    published_at = COALESCE(p.published_at, $1)
		FROM due
		WHERE p.id = due.id
		RETURNING p.id`,
		opts.Now, opts.Limit)
	if err != nil {
		return PublisherResult{}, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	res := PublisherResult{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			res.Errors++
			logger.WarnContext(ctx, "scheduler/publisher: row scan failed",
				slog.Any("err", err))
			continue
		}
		res.Scanned++
		res.Published++
	}
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("rows: %w", err)
	}
	return res, nil
}
