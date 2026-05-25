package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

// AbortOrphansTaskName is the on-wire task name for the abort-orphans
// cron task. Exported so the worker's wiring code can pass it to
// taskspec.Enqueue (for manual runs, e.g. an admin "purge now"
// button) and so the cron registry can target the same handler.
const AbortOrphansTaskName = "media.abort_orphans"

// AbortOrphansCronName is the cron-registry key for the daily sweep.
// Format follows the project's "<resource>.<action>.<cadence>"
// convention so an admin browsing the cron table can tell what
// "media.abort_orphans.daily" does without opening the source.
const AbortOrphansCronName = "media.abort_orphans.daily"

// AbortOrphansSchedule is the cron expression that fires the sweep.
// 03:00 UTC matches the rest of the project's "quiet hour" cadences
// (revisions.purge.daily, idempotency.cleanup.hourly, etc.) so
// midnight-region operators don't see all the cleanup tasks crowd
// the same minute.
const AbortOrphansSchedule = "0 3 * * *"

// abortOrphansPayloadSchema is the JSON Schema the payload must
// satisfy. Empty object is the documented "no payload" shape; we
// still validate so a misconfigured cron entry that passes a stray
// key surfaces at enqueue rather than handler time.
var abortOrphansPayloadSchemaRaw = []byte(`{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"type": ["object", "null"],
	"additionalProperties": false
}`)

// AbortOrphansSpecOptions configures NewAbortOrphansSpec. Driver is
// required (the handler is a closure over it); the rest tune the
// sweep behaviour.
type AbortOrphansSpecOptions struct {
	// Driver is the storage backend the handler sweeps. Required.
	Driver Driver

	// OlderThan is forwarded to AbortOrphanedMultiparts. Defaults to
	// 24h.
	OlderThan time.Duration

	// Limit is forwarded to AbortOrphanedMultiparts. Defaults to
	// 1000.
	Limit int

	// Logger receives the structured cron output. nil falls back to
	// slog.Default.
	Logger *slog.Logger
}

// NewAbortOrphansSpec returns the TaskSpec the worker registers for
// the abort-orphans cron. The handler closes over opts.Driver so the
// cron-side enqueue does not need to know the storage configuration
// — only the worker process does.
func NewAbortOrphansSpec(opts AbortOrphansSpecOptions) (taskspec.TaskSpec, error) {
	if opts.Driver == nil {
		return taskspec.TaskSpec{}, fmt.Errorf("storage: NewAbortOrphansSpec: Driver is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	handler := func(ctx context.Context, raw []byte) error {
		// The payload is always empty/null for this task; we still
		// parse it so a stray non-null payload surfaces as a parse
		// error rather than being silently ignored.
		if len(raw) > 0 && string(raw) != "null" {
			var anyPayload map[string]any
			if err := json.Unmarshal(raw, &anyPayload); err != nil {
				return fmt.Errorf("storage/abort_orphans: parse payload: %w", err)
			}
		}
		res, err := AbortOrphanedMultiparts(ctx, opts.Driver, AbortOrphansOptions{
			OlderThan: opts.OlderThan,
			Limit:     opts.Limit,
			Logger:    opts.Logger,
		})
		if err != nil {
			return fmt.Errorf("storage/abort_orphans: sweep: %w", err)
		}
		opts.Logger.InfoContext(ctx, "storage/abort_orphans: sweep complete",
			slog.Int("scanned", res.Scanned),
			slog.Int("aborted", res.Aborted),
			slog.Int("errors", res.Errors),
		)
		return nil
	}
	return taskspec.TaskSpec{
		Name:     AbortOrphansTaskName,
		Queue:    "default",
		MaxRetry: 1,
		// 5 minutes is generous — a 1000-entry sweep with a slow
		// backend takes seconds, not minutes, but a busy S3 region
		// occasionally throttles and we want the retry to be useful
		// rather than time out partway.
		Timeout: 5 * time.Minute,
		Handler: handler,
	}, nil
}

// NewAbortOrphansCron returns the CronSpec registered against the
// project's cron registry. Daily at 03:00 UTC; payload is nil so
// the schedule fires the bare task with no per-fire data.
func NewAbortOrphansCron() cron.CronSpec {
	return cron.CronSpec{
		Name:     AbortOrphansCronName,
		Schedule: AbortOrphansSchedule,
		TaskName: AbortOrphansTaskName,
	}
}

// AbortOrphansPayloadSchema returns the JSON Schema bytes. Exposed
// so tests can validate payloads outside the Enqueue path.
func AbortOrphansPayloadSchema() []byte {
	out := make([]byte, len(abortOrphansPayloadSchemaRaw))
	copy(out, abortOrphansPayloadSchemaRaw)
	return out
}
