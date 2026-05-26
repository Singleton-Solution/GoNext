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

// GCTaskName is the on-wire task name for the trash-retention sweep.
const GCTaskName = "content.gc"

// GCCronName is the cron-registry key for the daily fire.
const GCCronName = "content.gc.daily"

// GCSchedule is the cron expression that fires the GC. 03:30 UTC
// trails the existing revisions.purge.daily (03:00 UTC) by half an
// hour so the two heavy queries don't pile up at the same minute.
const GCSchedule = "30 3 * * *"

// DefaultRetention is the trash-window the GC enforces when no
// override is provided. 30 days matches docs/01-core-cms.md §6 (the
// "trash auto-empty" contract the admin UI advertises).
const DefaultRetention = 30 * 24 * time.Hour

// DefaultGCBatchLimit caps how many rows a single fire deletes. The
// cap is here for the same reason the publisher has one: a sudden
// backlog (e.g. a bulk trash from the admin UI thirty days ago)
// would otherwise lock the table.
const DefaultGCBatchLimit = 1000

// gcPayloadSchemaRaw constrains the payload. Same shape as the
// publisher's — null or empty object.
var gcPayloadSchemaRaw = []byte(`{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type": ["object", "null"],
	"additionalProperties": false
}`)

// GCResult is the per-run summary the handler logs.
type GCResult struct {
	// Deleted is the number of rows hard-deleted from posts.
	Deleted int
}

// GCSpecOptions configures NewGCSpec.
type GCSpecOptions struct {
	// Pool is the pgx connection pool. Required.
	Pool *pgxpool.Pool

	// Retention is how long a trashed post sticks around before
	// being hard-deleted. Defaults to DefaultRetention.
	Retention time.Duration

	// Limit is the maximum number of rows per fire. Defaults to
	// DefaultGCBatchLimit.
	Limit int

	// Logger receives the structured per-fire output.
	Logger *slog.Logger

	// Now is a clock override for tests. Nil defaults to time.Now.
	Now func() time.Time
}

// NewGCSpec returns the TaskSpec the worker registers for the GC.
func NewGCSpec(opts GCSpecOptions) (taskspec.TaskSpec, error) {
	if opts.Pool == nil {
		return taskspec.TaskSpec{}, errors.New("scheduler: NewGCSpec: Pool is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Retention <= 0 {
		opts.Retention = DefaultRetention
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultGCBatchLimit
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	handler := func(ctx context.Context, raw []byte) error {
		if len(raw) > 0 && string(raw) != "null" {
			var anyPayload map[string]any
			if err := json.Unmarshal(raw, &anyPayload); err != nil {
				return fmt.Errorf("scheduler/gc: parse payload: %w", err)
			}
		}
		res, err := SweepTrash(ctx, opts.Pool, SweepOptions{
			Retention: opts.Retention,
			Limit:     opts.Limit,
			Now:       opts.Now(),
			Logger:    opts.Logger,
		})
		if err != nil {
			return fmt.Errorf("scheduler/gc: %w", err)
		}
		opts.Logger.InfoContext(ctx, "scheduler/gc: sweep complete",
			slog.Int("deleted", res.Deleted),
			slog.Duration("retention", opts.Retention))
		return nil
	}
	return taskspec.TaskSpec{
		Name:     GCTaskName,
		Queue:    "default",
		MaxRetry: 1,
		// 10 minutes is generous — the candidate set is small in
		// the steady state (only what trashed thirty days ago).
		// Larger backlogs spill into the next day's fire.
		Timeout: 10 * time.Minute,
		Handler: handler,
	}, nil
}

// NewGCCron returns the CronSpec registered against the cron
// registry. Daily at 03:30 UTC.
func NewGCCron() cron.CronSpec {
	return cron.CronSpec{
		Name:     GCCronName,
		Schedule: GCSchedule,
		TaskName: GCTaskName,
	}
}

// GCPayloadSchema returns the JSON Schema bytes for the GC payload.
func GCPayloadSchema() []byte {
	out := make([]byte, len(gcPayloadSchemaRaw))
	copy(out, gcPayloadSchemaRaw)
	return out
}

// SweepOptions tunes one SweepTrash invocation.
type SweepOptions struct {
	// Retention is the cutoff: rows with status='trash' and
	// updated_at <= now - Retention are hard-deleted.
	Retention time.Duration

	// Limit caps the number of rows deleted per call.
	Limit int

	// Now is the reference time for the retention comparison.
	Now time.Time

	// Logger is the structured logger for per-row warnings.
	Logger *slog.Logger
}

// SweepTrash hard-deletes posts whose status has been 'trash' for
// at least Retention. The cutoff is computed against the row's
// updated_at; the trigger that maintains updated_at on every UPDATE
// is what makes this work — moving a row TO trash is itself an
// UPDATE that resets the clock, so we measure from the last move-
// to-trash event rather than the original creation.
//
// The delete cascades into the row's revisions, autosaves, and
// other derived rows via ON DELETE CASCADE FKs declared in the
// schema. We do NOT delete media attachments referenced by the
// post; the abort-orphans sweep is what cleans those up once
// nothing references them.
//
// Returns the count of deleted rows. An error here is a real
// database failure (the candidate query or the delete failed) and
// is propagated to the task layer for retry.
func SweepTrash(ctx context.Context, pool *pgxpool.Pool, opts SweepOptions) (GCResult, error) {
	if pool == nil {
		return GCResult{}, errors.New("scheduler: SweepTrash: pool is required")
	}
	if opts.Retention <= 0 {
		return GCResult{}, errors.New("scheduler: SweepTrash: Retention must be positive")
	}
	if opts.Limit <= 0 {
		return GCResult{}, errors.New("scheduler: SweepTrash: Limit must be positive")
	}
	if opts.Now.IsZero() {
		return GCResult{}, errors.New("scheduler: SweepTrash: Now is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	cutoff := opts.Now.Add(-opts.Retention)
	// Same CTE shape as the publisher: SELECT … FOR UPDATE SKIP
	// LOCKED so two replicas firing the same minute don't trip
	// over each other. The cron-leader election (#88/#258) is
	// supposed to prevent the double-fire entirely, but the
	// belt-and-suspenders here is cheap.
	rows, err := pool.Query(ctx, `
		WITH expired AS (
			SELECT id
			FROM posts
			WHERE status = 'trash'
			  AND updated_at <= $1
			ORDER BY updated_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM posts p
		USING expired
		WHERE p.id = expired.id
		RETURNING p.id`,
		cutoff, opts.Limit)
	if err != nil {
		return GCResult{}, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	res := GCResult{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			logger.WarnContext(ctx, "scheduler/gc: row scan failed",
				slog.Any("err", err))
			continue
		}
		res.Deleted++
	}
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("rows: %w", err)
	}
	return res, nil
}
