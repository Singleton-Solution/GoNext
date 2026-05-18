package health

import (
	"strings"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// Plugin-latency histogram buckets, in seconds. Plugin invocations are
// expected to sit in the same range as HTTP request handlers — most
// hook bodies complete in single-digit milliseconds, the long tail
// matters for alerting, and anything past ten seconds is almost
// certainly trapping. We reuse metrics.HTTPLatencyBuckets so the
// histogram is comparable to gonext_http_request_duration_seconds in
// Grafana without bucket-mismatch warnings.
var pluginLatencyBuckets = metrics.HTTPLatencyBuckets

// Result names for the invocations_total counter. The full catalog
// matches hooks.ResultStatus.String() so a Prometheus query can be
// written against the same vocabulary the bridge logs. The "ok" label
// is the only non-error value; the "trap" label is reserved for
// host-synthesised trap reports.
const (
	ResultOK          = "ok"
	ResultError       = "error"
	ResultTrap        = "trap"
	ResultOutOfMemory = "out_of_memory"
	ResultBadPayload  = "bad_payload"
	ResultUnknownHook = "unknown_hook"
)

// Recorder is the narrow observer the runtime and capability checker
// call into to publish plugin telemetry.
//
// Defining this as an interface — rather than handing producers the
// concrete *Recorder type — keeps the import graph acyclic. The
// runtime package, the hook bridge, and the capability checker all
// import this interface and call it through whatever value the host
// process wires in; they never depend on the metrics registry or the
// ring buffer directly.
//
// Recorder is safe for concurrent use. Production wiring (NewRecorder)
// returns a *Recorder that satisfies this interface; tests can plug in
// a recording fake.
type Recorder interface {
	// ObserveInvocation records one hook dispatch. duration is the
	// wall-clock time the call took; result is one of the Result*
	// constants. The recorder both increments the counter and
	// observes the histogram from a single call so the producer
	// can't accidentally publish one and forget the other.
	ObserveInvocation(plugin, hook, result string, duration time.Duration)

	// ObserveTrap records one trap event. reason is the trap reason
	// from runtime.TrapError; the recorder normalises it to a
	// low-cardinality token before labelling the counter. detail
	// carries the unredacted reason and any caller-supplied
	// metadata (hook name, plugin args summary) for the ring
	// buffer.
	ObserveTrap(plugin string, reason string, detail TrapDetail)

	// ObserveCapabilityDenied records one capability denial. The
	// capability ID is the canonical cap registry key (e.g.
	// "http.fetch").
	ObserveCapabilityDenied(plugin, capability string)
}

// metricsSet is the Prometheus surface owned by a *Recorder. One
// per Recorder instance; constructed in NewRecorder via the supplied
// *metrics.Registry. Producers never touch this struct directly —
// they call the Recorder methods, which forward to these collectors.
type metricsSet struct {
	invocations       *prometheus.CounterVec
	duration          *prometheus.HistogramVec
	traps             *prometheus.CounterVec
	capabilityDenials *prometheus.CounterVec
}

// newMetricsSet registers the four plugin-health collectors against
// the supplied metrics Registry. The registry MUST be non-nil; passing
// nil panics (it would silently swallow every observation otherwise,
// which is the worst possible failure mode for an observability
// surface).
//
// The function is split out so tests can inject a private Registry
// without re-implementing the full Recorder.
func newMetricsSet(reg *metrics.Registry) *metricsSet {
	if reg == nil {
		panic("health.newMetricsSet: registry is required")
	}
	return &metricsSet{
		invocations: reg.NewCounter(
			"gonext_plugin_invocations_total",
			"Plugin hook dispatches, labelled by plugin slug, hook name, and result.",
			"plugin", "hook", "result",
		),
		duration: reg.NewHistogram(
			"gonext_plugin_duration_seconds",
			"Plugin hook dispatch duration, by plugin and hook.",
			pluginLatencyBuckets,
			"plugin", "hook",
		),
		traps: reg.NewCounter(
			"gonext_plugin_traps_total",
			"Plugin trap events, labelled by plugin slug and normalised reason token.",
			"plugin", "reason",
		),
		capabilityDenials: reg.NewCounter(
			"gonext_plugin_capability_denied_total",
			"Plugin capability denials, labelled by plugin slug and capability id.",
			"plugin", "capability",
		),
	}
}

// normaliseReason turns a free-form trap reason into a bounded-
// cardinality label. The wazero trap text is rich ("wasm error:
// integer divide by zero", "stack overflow", "out of bounds memory
// access at offset 42") and would explode the label space if used
// verbatim.
//
// The rules below pick out the leading category word(s) and drop
// anything past it. Unknown reasons collapse to "other" so the
// label set is finite regardless of what wazero emits.
func normaliseReason(reason string) string {
	r := strings.ToLower(strings.TrimSpace(reason))
	if r == "" {
		return "unknown"
	}
	switch {
	case strings.Contains(r, "integer divide"):
		return "integer_divide_by_zero"
	case strings.Contains(r, "stack overflow"), strings.Contains(r, "stack exhausted"):
		return "stack_overflow"
	case strings.Contains(r, "out of bounds"):
		return "out_of_bounds"
	case strings.Contains(r, "unreachable"):
		return "unreachable"
	case strings.Contains(r, "panic"):
		// Check "panic" before the OOM bucket so a wrapped
		// "panic: out of memory" still surfaces as a panic
		// (the panic message is the operator-facing signal).
		return "panic"
	case strings.Contains(r, "out of memory"), strings.Contains(r, " oom"), strings.HasPrefix(r, "oom"):
		return "out_of_memory"
	case strings.Contains(r, "context"):
		return "context_cancelled"
	case strings.Contains(r, "fuel"):
		return "fuel_exhausted"
	}
	return "other"
}
