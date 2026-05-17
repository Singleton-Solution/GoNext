package taskspec

import (
	"context"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// TaskSpec is the single source-of-truth declaration for one background
// task type — its on-wire name, the queue it lands on, its retry/timeout
// policy, the JSON Schema its payload must satisfy, and the handler that
// runs it on the worker side.
//
// One TaskSpec value describes one logical task end-to-end. The producer
// side (Enqueue) consults the spec to choose the queue, apply the retry
// policy, and validate the payload before it goes into Redis. The
// consumer side (Dispatch) consults the same spec to wire the handler
// onto an asynq.ServeMux with the matching options. Because both ends
// dereference the same struct, drift between them is structural rather
// than coordinated — you can't change MaxRetry in one place and forget
// to update the other.
//
// TaskSpec is intentionally a value, not an interface. The fields are
// declarative; behavior lives only in Handler. The handler signature is
// (ctx, payload []byte) rather than (ctx, *asynq.Task) so the asynq
// dependency does not leak into every package that declares a spec —
// the dispatch layer adapts payload []byte to *asynq.Task once, at the
// edge.
//
// A TaskSpec is registered into a Registry exactly once at process init,
// then read-only thereafter. Mutating a spec after registration is a bug
// the registry cannot detect (the value is stored by value, but the
// PayloadSchema pointer is shared); callers should treat the value as
// frozen after Register returns.
type TaskSpec struct {
	// Name is the on-wire task type, e.g. "webhook.deliver", "email.send",
	// "media.thumbnail". Used as the asynq task typename and as the key
	// in the registry's map; must be non-empty for Register to accept it.
	// Choose names with a "<resource>.<action>" shape for consistency with
	// the capability registry — the two registries are conceptually a
	// pair, and operators see both in the admin UI.
	Name string

	// Queue is the asynq queue this task lands on. The worker process
	// is configured to drain queues at different priorities; common
	// values are "critical", "default", "low". Empty Queue means "use
	// the asynq default queue" — Enqueue still passes the option so the
	// queue selection is deterministic and visible in the task info.
	Queue string

	// MaxRetry is the number of times asynq will re-enqueue this task on
	// handler error before sending it to the archived set. Zero means
	// no retry (one-shot); negative values are clamped to zero by asynq.
	// The retry schedule is asynq's default exponential backoff unless
	// the worker is wired with a custom RetryDelayFunc.
	MaxRetry int

	// Timeout bounds a single handler invocation. asynq cancels the
	// context once Timeout elapses; the handler should observe ctx.Done.
	// Zero Timeout means no per-invocation deadline — usually wrong for
	// network-touching tasks. Set this to a value that bounds the
	// expected work plus a reasonable buffer; the retry path covers the
	// "took too long this time" case.
	Timeout time.Duration

	// PayloadSchema is the JSON Schema (draft 2020-12, via
	// packages/go/jsonschemautil) that every payload enqueued for this
	// task must satisfy. Validation runs at Enqueue time on the producer,
	// not at handler entry on the consumer — invalid payloads should
	// never reach the queue. The handler may still defensively parse;
	// it should not re-validate against the schema.
	//
	// nil PayloadSchema means "no schema-level constraint", which is
	// allowed for legacy specs but discouraged. Prefer a permissive
	// schema (e.g. `{"type":"object"}`) over nil so the validation
	// pathway is always exercised.
	PayloadSchema *jsonschema.Schema

	// Handler is the function asynq invokes when a task of this type is
	// dequeued. It receives the raw payload bytes (the same bytes
	// passed to Enqueue) and the asynq-managed context. A nil return
	// means success; a non-nil error triggers asynq's retry path (up
	// to MaxRetry). To skip retries on a permanent error, the handler
	// should return an error wrapping asynq.SkipRetry — Dispatch does
	// not interpret the error itself.
	//
	// Handler must be set for any spec that will be registered onto a
	// ServeMux via Dispatch. A nil Handler is accepted by the Registry
	// (the producer-only case) but Dispatch will skip the spec rather
	// than panic, so a wiring bug surfaces as "task not handled" rather
	// than a nil dereference.
	Handler func(ctx context.Context, payload []byte) error
}
