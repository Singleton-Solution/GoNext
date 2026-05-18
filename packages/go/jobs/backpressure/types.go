package backpressure

import "errors"

// Priority categorizes a unit of work for the shedding decision. Higher
// numeric values are more important; the Gate treats the enum as ordered
// (Important > Normal, etc.) rather than as opaque labels. Adding a new
// priority means inserting it at the correct numeric position relative
// to the existing four — callers using ≥ Important compare against the
// constant by name, but the underlying ordering matters for the gate
// logic.
//
// We use int (not iota with no explicit values) and pin each constant so
// reordering the source lines doesn't silently change priorities. The
// zero value is Background on purpose: a caller that forgets to set a
// priority gets the most-shed treatment, which fails closed.
type Priority int

const (
	// Background is shed first. Suitable for non-time-sensitive
	// rollups, prefetch, cache warmers — anything that can be deferred
	// indefinitely without user-visible impact.
	Background Priority = 0

	// Normal is the default for run-of-the-mill async work like outbound
	// webhook delivery or non-critical email. Shed above SoftLimit.
	Normal Priority = 10

	// Important survives moderate backlogs. Use for traffic where
	// delaying loses value but a brief degraded-mode pause is
	// acceptable — e.g. transactional emails that are worth retrying
	// once Redis recovers but should not pile up if it's an outage.
	Important Priority = 20

	// Critical is never shed. Reserve for security-critical flows
	// (password reset, 2FA, signup verification) whose failure is a
	// worse outcome than letting them contend with backlog drain.
	Critical Priority = 30
)

// String returns the canonical lower-case label used for the
// Prometheus priority label and for log lines. Unknown numeric values
// fall through to "unknown" so a forgotten enum addition still produces
// a usable metric (the alert can fire on the unexpected series).
func (p Priority) String() string {
	switch p {
	case Background:
		return "background"
	case Normal:
		return "normal"
	case Important:
		return "important"
	case Critical:
		return "critical"
	default:
		return "unknown"
	}
}

// Threshold pins the soft/hard pending-task depths for one queue. The
// gate uses these to decide whether to admit a given (queue, priority)
// pair. A Threshold value is read-only after registration with the
// Gate; tests construct fresh values per-case.
//
// Invariants enforced by the Gate at construction:
//
//   - Queue must be non-empty.
//   - 0 < SoftLimit ≤ HardLimit. Equal values are allowed (collapse the
//     middle band) — useful for queues that should jump straight from
//     "accept everything" to "critical only".
//   - Non-positive limits are rejected; we'd rather fail loudly than
//     silently shed (limit=0) or never shed (limit=MaxInt by accident).
type Threshold struct {
	// Queue is the asynq queue name (one of the canonical seven, e.g.
	// "webhook", "email"). Matched verbatim against the queue argument
	// to Gate.Allow.
	Queue string

	// SoftLimit is the depth at and above which Normal/Background
	// traffic is shed. Important and Critical still pass.
	SoftLimit int

	// HardLimit is the depth at and above which only Critical passes.
	// Must be ≥ SoftLimit; the Gate validates this at construction.
	HardLimit int
}

// ErrShed is the sentinel returned by Gate.Allow when the depth exceeds
// the threshold for the given priority. Callers should errors.Is-check
// this rather than comparing strings — wrapping is allowed (the
// middleware wraps it with the queue/priority context for log lines).
//
// 429 Too Many Requests is the HTTP mapping the middleware uses; for
// non-HTTP callers, ErrShed is the signal to short-circuit into the
// degraded-mode branch (skip the webhook, drop the email, etc.).
var ErrShed = errors.New("backpressure: shed by queue depth gate")

// ErrInvalidThreshold is returned by NewGate when a Threshold value
// violates an invariant. The wrapped message names the offending queue
// so the caller can correct their wiring without grepping.
var ErrInvalidThreshold = errors.New("backpressure: invalid threshold")
