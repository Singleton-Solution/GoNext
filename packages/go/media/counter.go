package media

// Counter is the minimal hook the Coalescer uses to emit metrics.
//
// One method, Inc(name), so any backend can satisfy it without the
// media package importing a metrics library. Production callers wire
// this to a Prometheus *CounterVec via a tiny adapter; tests use the
// MemoryCounter type in this package.
//
// Implementations MUST be safe for concurrent use — the Coalescer
// calls Inc from any goroutine that completes a Get.
//
// The two names the Coalescer emits are:
//
//   - "media_variant_coalesce_total" — incremented once per follower
//     (a caller that attached to an in-flight generation started by
//     another caller).
//
//   - "media_variant_generate_total" — incremented once per leader
//     (a caller that actually executed the generate function).
//
// Adapters live next to their consumers — for example, the HTTP
// service that wires the Coalescer creates a CounterVec via
// packages/go/metrics.Registry.NewCounter and supplies an adapter
// whose Inc(name) calls vec.WithLabelValues(name).Inc(). Keeping the
// adapter out of this package is what lets the media package stay
// dep-free of Prometheus while still being observable in production.
type Counter interface {
	Inc(name string)
}

// nopCounter is the zero-cost default used when CoalescerOptions.Counter
// is nil. Production code is expected to supply a real Counter; the nop
// lets unit tests construct a Coalescer without ceremony.
type nopCounter struct{}

func (nopCounter) Inc(string) {}
