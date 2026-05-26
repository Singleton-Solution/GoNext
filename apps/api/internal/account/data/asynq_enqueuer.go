package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// asyncTaskName is the canonical task name the worker registers
// (see apps/worker/internal/tasks/gdpr.TaskExportRun). We keep the
// string literal here rather than import the worker package — that
// would create an apps-cross-apps dependency the build doesn't allow.
const asyncTaskName = "gdpr.export.run"

// AsynqEnqueuer is the production [ExportEnqueuer] backed by Asynq's
// client. The caller owns the underlying client's lifecycle; we hold
// only a borrowed reference.
type AsynqEnqueuer struct {
	client *asynq.Client
	queue  string
}

// NewAsynqEnqueuer wraps a client. Queue defaults to "default" if
// empty — operators who want a dedicated GDPR queue (matching the
// "critical" queue used by the purge tick) pass it through.
func NewAsynqEnqueuer(client *asynq.Client, queue string) *AsynqEnqueuer {
	if client == nil {
		panic("data.NewAsynqEnqueuer: client is required")
	}
	if queue == "" {
		queue = "default"
	}
	return &AsynqEnqueuer{client: client, queue: queue}
}

// payload mirrors gdpr.ExportPayload (in apps/worker/internal/tasks/gdpr).
// Keeping a private copy of the wire shape lets us avoid the
// apps-cross-apps import while still serialising the exact JSON the
// worker decodes.
type payload struct {
	UserID string `json:"user_id"`
	JobID  string `json:"job_id"`
}

// Enqueue implements [ExportEnqueuer].
func (e *AsynqEnqueuer) Enqueue(ctx context.Context, userID, jobID string) error {
	body, err := json.Marshal(payload{UserID: userID, JobID: jobID})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	t := asynq.NewTask(asyncTaskName, body)
	if _, err := e.client.EnqueueContext(ctx, t,
		asynq.Queue(e.queue),
		asynq.Retention(7*24*time.Hour),
		asynq.MaxRetry(3),
		// Uniqueness window: a second export request from the same
		// user inside this window collapses into the first task
		// instead of producing a duplicate ZIP.
		asynq.Unique(time.Hour),
	); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	return nil
}
