package jobs

import (
	"encoding/json"
	"fmt"
)

// Payload envelopes the guest receives carry both the producer-supplied
// payload bytes AND the asynq Task ID. We need the Task ID inside the
// guest because:
//
//   - The plugin's job handler may have idempotent side effects (post
//     a webhook, write a row keyed by external ID, etc.) and needs a
//     stable per-invocation key to dedupe. Asynq's Task ID is stable
//     across retries of the same task (the ID is generated at enqueue
//     time and reused on every retry), so it's the natural idempotency
//     key.
//
//   - The plugin SDK cannot reach into the host's idempotency store
//     directly — that's an out-of-bounds call from inside the WASM
//     sandbox. Threading the key through the payload envelope keeps the
//     guest self-contained.
//
// We intentionally do NOT wrap the inner payload in another JSON layer
// when the plugin doesn't want JSON: the wire format is "envelope JSON
// containing payload bytes as a base64 string OR a raw JSON value".
// Plugins that already speak JSON read .payload as a JSON value
// directly; plugins that ship binary payloads decode .payload from
// base64. The convention is the plugin SDK's responsibility — the host
// just passes bytes through.
//
// For v1 we keep the envelope simple: the producer-side enqueue
// validates against TaskSpec.PayloadSchema, so the bytes the guest
// sees are guaranteed JSON. The envelope wraps those bytes verbatim
// in the .payload field.

// JobEnvelope is the wire form of a job invocation as seen by the
// guest. Encoded once per dispatch by the host bridge.
//
//   - IdempotencyKey is the asynq Task ID (stable across retries of the
//     same task). Plugins use it to dedupe side effects. Empty when the
//     bridge could not extract a Task ID from the context (a test path
//     calling InvokeJob directly without an asynq context).
//
//   - RetryCount is the asynq-reported retry counter (0 for the first
//     attempt). Plugins that want to back off, log differently on
//     retries, or short-circuit after N attempts inspect this.
//
//   - Payload is the producer-supplied payload bytes verbatim. For
//     correctly-built plugins this is valid JSON (the schema check
//     enforces that at enqueue time), but the envelope does not
//     re-validate — the bytes pass through untouched.
type JobEnvelope struct {
	IdempotencyKey string          `json:"idempotency_key"`
	RetryCount     int             `json:"retry_count"`
	Payload        json.RawMessage `json:"payload"`
}

// MarshalJobEnvelope returns the JSON wire bytes for a job invocation.
//
// payload may be nil — the producer is allowed to enqueue a "no body"
// task (e.g. a scheduled "ping every minute" job). The encoded form is
// always a complete envelope; "payload" carries the literal JSON null
// in that case so the guest's decoder has a single shape to handle.
//
// idempotencyKey is the asynq Task ID. Empty is permitted (test
// surface, or a producer that didn't go through asynq); the guest can
// detect it and fall back to its own dedup scheme.
//
// retryCount is the asynq retry counter (0 on first attempt). Negative
// values are normalized to 0 — asynq's API guarantees non-negative, but
// the bridge defends against a bad caller.
//
// Error: returns a non-nil error only if encoding/json itself fails on
// the envelope shape (essentially impossible — the fields are scalars
// and a json.RawMessage). The error path is here for symmetry with the
// hooks marshal API.
func MarshalJobEnvelope(idempotencyKey string, retryCount int, payload []byte) ([]byte, error) {
	if retryCount < 0 {
		retryCount = 0
	}
	if payload == nil {
		payload = json.RawMessage("null")
	}
	env := JobEnvelope{
		IdempotencyKey: idempotencyKey,
		RetryCount:     retryCount,
		Payload:        payload,
	}
	buf, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("abi/jobs: marshal envelope: %w", err)
	}
	return buf, nil
}
