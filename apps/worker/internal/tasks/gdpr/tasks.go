// Package gdpr — see doc.go for the package overview.
package gdpr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

// Task names. Exported so the API enqueue path and the worker registry
// agree on the same canonical strings without re-typing the magic
// value.
const (
	TaskExportRun = "gdpr.export.run"
	TaskPurgeTick = "gdpr.purge.tick"
)

// PurgeCronName is the registry name for the purge sweep. Exposed so
// the metrics dashboards and the leader-election lease can refer to
// the same key.
const PurgeCronName = "gdpr.purge.tick"

// PurgeCronSchedule is the cron expression for the purge sweep.
// Every 10 minutes keeps the worst-case latency between
// "scheduled_purge_at fires" and "rows are gone" under the 15-minute
// floor our DPA promises, while staying well below the hourly cap on
// the worker's cron lease budget.
const PurgeCronSchedule = "@every 10m"

// ExportPayload is the JSON payload enqueued by the API's export
// handler. The Job ID is the same opaque token returned to the user
// for polling.
type ExportPayload struct {
	UserID string `json:"user_id"`
	JobID  string `json:"job_id"`
}

// PurgePayload is intentionally empty: the purge tick reads "now" and
// the database state at fire time. We keep the type so the taskspec
// registry can still pin a payload schema if we later want to add a
// dry-run flag.
type PurgePayload struct {
	// DryRun, if true, asks the handler to log what it WOULD delete
	// without actually mutating the database. Useful for the operator
	// runbook smoke-test (`gonext jobs run --dry-run gdpr.purge.tick`).
	DryRun bool `json:"dry_run,omitempty"`
}

// PurgeStore is the database-side contract for the purge sweep.
// Pulling only the methods we use makes the package testable without
// a Postgres dependency — an in-memory fake satisfies it.
type PurgeStore interface {
	// SelectDuePurges returns user ids whose scheduled_purge_at is at
	// or before `now`. The handler caps the batch size to keep the
	// transaction short.
	SelectDuePurges(ctx context.Context, now time.Time, limit int) ([]string, error)

	// HardDelete removes the user row and every cascade-attached
	// record. Implementations MUST run this inside a transaction.
	HardDelete(ctx context.Context, userID string) error
}

// ExportStore is the database-side contract for the export handler.
type ExportStore interface {
	// AssembleExport gathers every table the user owns and uploads a
	// ZIP to the configured object store, returning the public URL of
	// the artifact. The implementation is responsible for serialising,
	// zipping, and uploading — keeping it behind an interface keeps
	// this file small and testable.
	AssembleExport(ctx context.Context, userID, jobID string) (url string, err error)

	// MarkExportReady writes the artifact URL onto the export-job row
	// so the REST polling endpoint can surface it. Errors here are
	// fatal: the user is owed a downloadable artifact, and if we
	// can't surface it, retry policy should re-run the whole task.
	MarkExportReady(ctx context.Context, jobID, url string) error
}

// Deps is the constructor input for Specs. Logger may be nil
// (defaults to slog.Default); both stores are required and panic if
// missing.
type Deps struct {
	Exports ExportStore
	Purges  PurgeStore
	Log     *slog.Logger

	// PurgeBatchSize is the maximum number of users purged per tick.
	// Zero falls back to 100 — a number small enough to keep the
	// transaction's lock window short, large enough to drain a
	// realistic backlog inside the 10-minute cadence.
	PurgeBatchSize int
}

const defaultPurgeBatchSize = 100

// Specs returns the taskspecs that handlers should register on the
// worker's asynq mux. Wire from apps/worker/cmd/worker/main.go:
//
//	for _, s := range gdpr.Specs(deps) {
//	    registry.Register(s)
//	}
//	taskspec.Dispatch(registry, mux)
//
// Why a slice rather than a single Register call: keeping the spec
// list explicit makes "what tasks exist in this binary" trivially
// greppable from main.go.
func Specs(d Deps) []taskspec.TaskSpec {
	if d.Exports == nil {
		panic("gdpr.Specs: Exports store is required")
	}
	if d.Purges == nil {
		panic("gdpr.Specs: Purges store is required")
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	batch := d.PurgeBatchSize
	if batch <= 0 {
		batch = defaultPurgeBatchSize
	}

	return []taskspec.TaskSpec{
		{
			Name:    TaskExportRun,
			Queue:   "default",
			Handler: makeExportHandler(d.Exports, log),
		},
		{
			Name:    TaskPurgeTick,
			Queue:   "critical",
			Handler: makePurgeHandler(d.Purges, log, batch),
		},
	}
}

// CronSpec returns the cron entry that fires the purge tick. Wire in
// the worker's main:
//
//	registry.Register(gdpr.CronSpec())
//
// The schedule is fixed at PurgeCronSchedule — operators who want a
// different cadence pass --cron-overrides on the worker (the override
// path is shared with every other cron task and lives outside this
// package).
func CronSpec() cron.CronSpec {
	return cron.CronSpec{
		Name:     PurgeCronName,
		Schedule: PurgeCronSchedule,
		TaskName: TaskPurgeTick,
		Payload:  PurgePayload{},
	}
}

// --- handlers ---------------------------------------------------------

func makeExportHandler(store ExportStore, log *slog.Logger) func(context.Context, []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var p ExportPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("gdpr.export: decode payload: %w", err)
		}
		if p.UserID == "" || p.JobID == "" {
			return fmt.Errorf("gdpr.export: missing user_id or job_id")
		}
		log.InfoContext(ctx, "gdpr.export: starting",
			slog.String("user_id", p.UserID),
			slog.String("job_id", p.JobID))

		url, err := store.AssembleExport(ctx, p.UserID, p.JobID)
		if err != nil {
			log.ErrorContext(ctx, "gdpr.export: assemble failed",
				slog.String("user_id", p.UserID),
				slog.String("job_id", p.JobID),
				slog.String("err", err.Error()))
			return fmt.Errorf("assemble: %w", err)
		}

		if err := store.MarkExportReady(ctx, p.JobID, url); err != nil {
			log.ErrorContext(ctx, "gdpr.export: mark ready failed",
				slog.String("job_id", p.JobID),
				slog.String("err", err.Error()))
			return fmt.Errorf("mark ready: %w", err)
		}

		log.InfoContext(ctx, "gdpr.export: completed",
			slog.String("user_id", p.UserID),
			slog.String("job_id", p.JobID))
		return nil
	}
}

func makePurgeHandler(store PurgeStore, log *slog.Logger, batch int) func(context.Context, []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var p PurgePayload
		if len(payload) > 0 && string(payload) != "null" {
			if err := json.Unmarshal(payload, &p); err != nil {
				return fmt.Errorf("gdpr.purge: decode payload: %w", err)
			}
		}

		now := time.Now().UTC()
		ids, err := store.SelectDuePurges(ctx, now, batch)
		if err != nil {
			return fmt.Errorf("select due purges: %w", err)
		}
		if len(ids) == 0 {
			log.DebugContext(ctx, "gdpr.purge: no rows due")
			return nil
		}

		if p.DryRun {
			log.InfoContext(ctx, "gdpr.purge: dry run",
				slog.Int("count", len(ids)),
				slog.Any("user_ids", ids))
			return nil
		}

		var purged, failed int
		for _, id := range ids {
			if err := store.HardDelete(ctx, id); err != nil {
				failed++
				log.WarnContext(ctx, "gdpr.purge: hard delete failed",
					slog.String("user_id", id),
					slog.String("err", err.Error()))
				continue
			}
			purged++
		}
		log.InfoContext(ctx, "gdpr.purge: swept",
			slog.Int("purged", purged),
			slog.Int("failed", failed),
			slog.Int("batch", batch),
		)
		if failed > 0 {
			// Returning an error lets asynq retry the tick. The next
			// fire re-selects rows that still have scheduled_purge_at
			// in the past, so successful deletes from this tick are
			// not retried.
			return fmt.Errorf("hard-delete failures: %d of %d", failed, len(ids))
		}
		return nil
	}
}
