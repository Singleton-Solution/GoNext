package jobs

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
)

// Inspector is the subset of *asynq.Inspector the handler depends on.
// Defined here so tests can supply a fake without standing up Redis or
// pulling in the real client. Production wiring passes *asynq.Inspector
// directly — it satisfies the interface because the method signatures
// match verbatim.
//
// The four methods cover the entire DLQ admin surface:
//
//   - ListArchivedTasks: feeds the list view (paginated).
//   - GetTaskInfo:       feeds the detail view (one task at a time).
//   - RunArchivedTask:   replay action; moves archived → pending.
//   - DeleteArchivedTask: discard action; removes the task entirely.
type Inspector interface {
	ListArchivedTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	GetTaskInfo(queue, id string) (*asynq.TaskInfo, error)
	RunArchivedTask(queue, id string) error
	DeleteArchivedTask(queue, id string) error
}

// RedactionStore persists per-task redaction records. Two backends:
// in-memory for tests; Postgres for production (migration #000017).
// Methods return ErrRedactionNotFound for missing rows so the listing
// handler can branch cleanly.
type RedactionStore interface {
	// Get returns the redaction record for the (queue, taskID) pair, or
	// ErrRedactionNotFound if no redaction has been applied. Used on the
	// hot path of the listing handler.
	Get(ctx context.Context, queue, taskID string) (Redaction, error)

	// GetMany returns redaction records for a batch of task IDs. The
	// returned map is keyed by task ID; tasks with no redaction are
	// absent from the map (not present as a zero-value Redaction). This
	// is the bulk variant used by the list handler so we don't fan out
	// one Get call per row.
	GetMany(ctx context.Context, queue string, taskIDs []string) (map[string]Redaction, error)

	// Upsert creates or replaces the redaction record for a task. The
	// caller supplies the full field set; we do not merge — an admin
	// who wants to remove a field reissues the action with the smaller
	// set. Empty fields slices are rejected at the handler boundary
	// (the migration's CHECK constraint enforces it at the DB layer).
	Upsert(ctx context.Context, r Redaction) error
}

// Redaction is a single row of the task_redactions table. We use the
// Go-side representation rather than reaching for a generic record type
// because the field count is small and a typed struct documents the
// contract.
type Redaction struct {
	TaskID      string    `json:"task_id"`
	Queue       string    `json:"queue"`
	Fields      []string  `json:"fields"`
	RedactedAt  time.Time `json:"redacted_at"`
	RedactedBy  string    `json:"redacted_by,omitempty"`
}

// HasField reports whether the given top-level payload field is in the
// redaction set. Case-sensitive — the JSON field names we redact must
// match the producer's casing exactly. O(len(Fields)) which is fine
// given the typical set is < 10 entries.
func (r Redaction) HasField(field string) bool {
	for _, f := range r.Fields {
		if f == field {
			return true
		}
	}
	return false
}

// ArchivedTask is the serialised form of an Asynq archived task as
// returned by the list/get endpoints. We translate from *asynq.TaskInfo
// to this struct rather than serialising the upstream type directly
// because:
//
//   - *asynq.TaskInfo carries fields irrelevant to the DLQ surface
//     (NextProcessAt, Group, IsOrphaned, etc.). Stripping them keeps
//     the JSON envelope tight and the OpenAPI contract narrow.
//   - We need to interleave the redaction record into the response, and
//     mutating the upstream struct's Payload would be surprising.
//   - It pins the on-wire shape against accidental upstream version
//     bumps that re-name or re-type a field.
type ArchivedTask struct {
	// ID is the Asynq-assigned task ID. Globally unique across queues.
	// This is what the action endpoints take as their path parameter.
	ID string `json:"id"`

	// Queue is the queue the task was archived from.
	Queue string `json:"queue"`

	// Type is the task type (e.g. "webhook:deliver"). Useful for
	// filtering in the UI.
	Type string `json:"type"`

	// PayloadPreview is the first 200 chars of the payload after
	// redaction has been applied. The full payload is on the detail
	// endpoint; the list view is intentionally compact so the table
	// stays readable on a typical laptop screen.
	PayloadPreview string `json:"payload_preview"`

	// Payload is the full payload bytes, present only on the detail
	// endpoint. On the list endpoint this is nil.
	Payload []byte `json:"payload,omitempty"`

	// LastError is the error message from the last failed attempt. Used
	// to drive the "why did this fail?" column in the UI.
	LastError string `json:"last_error"`

	// FailedAt is when the last failure occurred. Zero if the task was
	// archived for a non-error reason (max-retry exhaustion is the
	// usual cause and always sets this).
	FailedAt time.Time `json:"failed_at"`

	// Retried is how many times the task was retried before being
	// archived. Useful for triage ("retried 3/3 times" vs. "tripped
	// once and got nuked").
	Retried int `json:"retried"`

	// MaxRetry is the retry budget the task was configured with. Pairs
	// with Retried to render "n/m" in the UI.
	MaxRetry int `json:"max_retry"`

	// Redacted indicates the task has an active redaction record.
	// Drives the "redacted" badge in the UI. The actual fields are in
	// RedactedFields.
	Redacted bool `json:"redacted"`

	// RedactedFields is the set of payload field names that have been
	// masked. Empty when Redacted is false. Surfaced so the UI can
	// show "fields masked: email, api_key".
	RedactedFields []string `json:"redacted_fields,omitempty"`
}
