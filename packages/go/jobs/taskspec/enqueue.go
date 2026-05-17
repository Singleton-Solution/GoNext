package taskspec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"
)

// ErrUnknownTask is returned by Enqueue when the named spec is not
// present in the registry. The producer is asking for a task type that
// no package has declared, which is almost always a typo or a missing
// import in the wiring; surfacing a typed error lets the caller branch
// on it (e.g. a plugin host can fall back to a generic "unknown job"
// audit row instead of crashing).
var ErrUnknownTask = errors.New("taskspec: unknown task name")

// ErrNilClient is returned by Enqueue when the asynq client is nil.
// Tests and partially-wired binaries can hit this; we prefer a typed
// error over a nil-dereference panic so the failure shows up cleanly
// in handler error paths and in the integration-test harness.
var ErrNilClient = errors.New("taskspec: asynq client is nil")

// ErrInvalidPayload is returned by Enqueue when the payload fails
// JSON marshaling or fails validation against the spec's
// PayloadSchema. The wrapped error carries the validator's detail (or
// the json package's error) so the caller can render a useful message.
//
// Validating at Enqueue time is deliberate: a bad payload must never
// reach the queue, because the consumer side has no good way to surface
// "this payload doesn't match the schema" — by the time the handler
// runs, the originating request context is long gone. Catching it on
// the producer side, where the error can flow back to the caller as
// a 4xx, is the only honest place.
var ErrInvalidPayload = errors.New("taskspec: payload failed schema validation")

// Enqueue looks up name in registry, validates payload against the
// spec's PayloadSchema, and enqueues an asynq task with the spec's
// Queue / MaxRetry / Timeout options applied.
//
// payload may be a []byte (used verbatim) or any json.Marshal-able
// value (marshaled here). The marshaled bytes are what the handler
// receives on the consumer side and what the schema validates against,
// so the round-trip is byte-for-byte stable.
//
// Errors:
//
//   - ErrNilClient if client is nil.
//   - ErrUnknownTask (wrapped with name) if registry has no spec.
//   - ErrInvalidPayload (wrapped with validator detail) if the payload
//     fails schema validation. JSON marshaling failures wrap json's
//     error rather than ErrInvalidPayload — they are caller bugs (a
//     non-marshalable type), not schema violations.
//
// On success, returns the *asynq.TaskInfo from the underlying client.
//
// Safe for concurrent use; the registry lock is held only briefly.
func Enqueue(ctx context.Context, client *asynq.Client, registry *Registry, name string, payload any) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, ErrNilClient
	}
	if registry == nil {
		registry = Default()
	}
	spec, ok := registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTask, name)
	}

	// Normalize the payload to bytes. The []byte fast path matters for
	// callers that have already marshaled (e.g. webhook delivery passes
	// pre-signed bodies) — re-marshaling would change the bytes the
	// handler receives.
	body, err := normalizePayload(payload)
	if err != nil {
		return nil, fmt.Errorf("taskspec: marshal payload: %w", err)
	}

	// Validate. nil PayloadSchema is the documented "no constraint"
	// path; we skip validation but still proceed with the enqueue.
	if spec.PayloadSchema != nil {
		var instance any
		if err := json.Unmarshal(body, &instance); err != nil {
			return nil, fmt.Errorf("%w: payload is not valid JSON: %w", ErrInvalidPayload, err)
		}
		if err := spec.PayloadSchema.Validate(instance); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
		}
	}

	task := asynq.NewTask(spec.Name, body)
	opts := optionsFromSpec(spec)
	return client.EnqueueContext(ctx, task, opts...)
}

// optionsFromSpec maps the declarative fields on a TaskSpec to the
// imperative asynq.Option values the client expects. Extracted as a
// helper so Dispatch can reuse the same translation when it wires the
// handler onto a mux (asynq doesn't yet support per-pattern options on
// the mux, so Dispatch records the options for diagnostics rather than
// applying them — but the translation logic stays in one place).
//
// An empty Queue is omitted rather than passed as "" — asynq rejects
// the empty queue name with a validation error, so the omission is the
// only sensible mapping of "spec wants the default queue".
func optionsFromSpec(spec TaskSpec) []asynq.Option {
	opts := make([]asynq.Option, 0, 3)
	if spec.Queue != "" {
		opts = append(opts, asynq.Queue(spec.Queue))
	}
	if spec.MaxRetry > 0 {
		opts = append(opts, asynq.MaxRetry(spec.MaxRetry))
	}
	if spec.Timeout > 0 {
		opts = append(opts, asynq.Timeout(spec.Timeout))
	}
	return opts
}

// normalizePayload coerces an `any` payload into the byte form asynq
// expects on the wire. A []byte is used verbatim so callers that pre-
// marshal (or pre-sign) the body retain byte-exact control; any other
// type is run through json.Marshal. A nil payload becomes the literal
// JSON null — the schema can reject it if that's not allowed.
func normalizePayload(p any) ([]byte, error) {
	if b, ok := p.([]byte); ok {
		return b, nil
	}
	return json.Marshal(p)
}
