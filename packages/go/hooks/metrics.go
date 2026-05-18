package hooks

// Sink is the metrics-emission surface the Bus calls into.
//
// The bus intentionally does NOT depend on packages/go/metrics — that would
// invert the layering (metrics depends on prometheus/client_golang, which we
// don't want to drag into every package that wants to fire a hook). Instead
// the bus calls into a tiny interface that packages/go/metrics can later
// implement (or that an operator can stub out for tests).
//
// All methods MUST be safe for concurrent use. Implementations are
// expected to be effectively free at the call site (lock-free counter
// increment, histogram observation via a pre-allocated bucket array);
// the bus does not batch or defer because most call sites are not on a
// tight loop.
//
// The default Bus uses noopSink, which discards every observation. Wire a
// real implementation with Bus.WithMetrics. The names handed to the sink
// are stable identifiers — see the docstrings on each method for the catalog.
type Sink interface {
	// Counter increments a counter by 1. The label map identifies the
	// hook name and (for actions) whether the dispatch was async. Names
	// emitted by the bus:
	//
	//   gonext_hooks_dispatch_total{kind=action,hook=...,async=true|false}
	//   gonext_hooks_dispatch_total{kind=filter,hook=...}
	//   gonext_hooks_handler_error_total{kind=...,hook=...}
	//   gonext_hooks_handler_panic_total{kind=...,hook=...}
	//   gonext_hooks_filter_short_circuit_total{hook=...}
	Counter(name string, labels map[string]string)

	// Histogram observes a value (typically a duration in seconds) into
	// the named histogram. Names emitted by the bus:
	//
	//   gonext_hooks_dispatch_duration_seconds{kind=...,hook=...}
	//   gonext_hooks_handler_duration_seconds{kind=...,hook=...}
	Histogram(name string, value float64, labels map[string]string)
}

// noopSink is the zero-value-friendly Sink used when none has been wired in.
// Methods are empty so the compiler can inline them away at the call site.
type noopSink struct{}

func (noopSink) Counter(string, map[string]string)                {}
func (noopSink) Histogram(string, float64, map[string]string)     {}

// Metric and label names emitted by the bus. Centralized so the metrics
// package's later glue can reference these constants rather than copying
// the literals.
const (
	metricDispatchTotal       = "gonext_hooks_dispatch_total"
	metricDispatchDuration    = "gonext_hooks_dispatch_duration_seconds"
	metricHandlerDuration     = "gonext_hooks_handler_duration_seconds"
	metricHandlerError        = "gonext_hooks_handler_error_total"
	metricHandlerPanic        = "gonext_hooks_handler_panic_total"
	metricShortCircuit        = "gonext_hooks_filter_short_circuit_total"
	// metricSchemaRejected counts dispatches refused by the
	// SchemaEnforcer (see Bus.WithSchemas). The bus increments this
	// before short-circuiting Do/ApplyFilters with the validator's
	// error. Used by ops to spot misbehaving plugins firing
	// malformed payloads.
	metricSchemaRejected = "gonext_hooks_schema_rejected_total"

	labelKind  = "kind"
	labelHook  = "hook"
	labelAsync = "async"

	kindAction = "action"
	kindFilter = "filter"
)
