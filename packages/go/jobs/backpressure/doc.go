// Package backpressure sheds non-critical enqueue traffic when an Asynq
// queue's pending depth exceeds operator-configured thresholds.
//
// The motivation is degraded-mode resilience: when the queue backs up
// (Redis slow, consumers behind, a deploy stalled), naively continuing to
// enqueue work makes the situation worse — every accepted task is one more
// thing that has to be drained before normal latency returns. The
// backpressure gate gives callers a typed error (ErrShed) on the producer
// side so they can apply graceful degradation: skip non-essential webhook
// retries, drop low-priority email, etc. Critical traffic (password reset,
// 2FA) is never shed.
//
// # Topology
//
// Three cooperating pieces:
//
//   - Monitor — polls *asynq.Inspector at a fixed interval, caches the
//     latest Pending count per queue in an atomic map. Cheap to read.
//
//   - Gate — pure decision function: given (queue, priority) and the
//     latest depth from a Monitor, returns nil or ErrShed based on the
//     SoftLimit / HardLimit pair configured for that queue.
//
//   - Middleware — wraps an HTTP enqueue endpoint; reads the priority
//     from the request (resolver supplied by the caller), consults the
//     Gate, and either lets the handler run or responds 429 with
//     ErrShed. Emits gonext_backpressure_shed_total on each shed.
//
// The split keeps the Gate trivially testable (no goroutines, no I/O)
// and lets non-HTTP callers (cron, internal queue producers) reuse it
// directly without dragging in net/http.
//
// # Priority semantics
//
// Four priorities, in shedding order from most-protected to least:
//
//	Critical   — never shed (password reset, 2FA, security alerts)
//	Important  — shed only above HardLimit (transactional email)
//	Normal     — shed above SoftLimit (standard webhook deliveries)
//	Background — shed above SoftLimit (analytics rollups, prefetch)
//
// The decision table:
//
//	depth < SoftLimit       → every priority passes
//	SoftLimit ≤ depth < Hard → Critical + Important pass; Normal/Background shed
//	depth ≥ HardLimit        → only Critical passes
//
// # Observability
//
// gonext_backpressure_shed_total{queue,priority} counts every shed
// decision. Operators alert on a sustained non-zero rate as a signal that
// the queue is in degraded-mode protection. Sustained Critical sheds
// (which the gate never produces but the counter would accept if a future
// caller wired one) would indicate a configuration bug — keep the alert
// on Important/Normal/Background series only.
package backpressure
