package runtime

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// MaxRegisteredPlugins caps how many distinct plugin slugs the metrics
// subsystem will accept before refusing new registrations. The bound
// exists to keep Prometheus storage manageable: every distinct slug
// label multiplies the series count across every plugin-labelled
// metric (fuel_consumed, timeout, abi_call, lifecycle, metric_observe).
//
// 100 is a generous default for a single-machine host — typical
// deployments run a handful of plugins, and a host running 100+
// distinct WASM modules is operating outside the design point we're
// optimising for. Operators that need more can construct their own
// PluginMetrics with NewPluginMetricsWithLimit.
const MaxRegisteredPlugins = 100

// ErrPluginSlugLimit is returned by PluginMetrics.RegisterSlug when
// the registry already holds MaxRegisteredPlugins distinct slugs.
// Operators see this surface as a "plugin temporarily unavailable"
// condition; the failing slug is logged so they can prune.
//
// The error wraps the slug name in its message — callers can match
// the typed sentinel via errors.Is.
type ErrPluginSlugLimit struct {
	Slug  string
	Limit int
}

func (e *ErrPluginSlugLimit) Error() string {
	return fmt.Sprintf("runtime/metrics: refusing to register slug %q: cardinality cap reached (%d distinct slugs)", e.Slug, e.Limit)
}

// PluginMetrics is the catalogue of Prometheus series the plugin host
// emits. One instance per Runtime is the intended pattern — the
// Runtime holds the *PluginMetrics and every host function reaches
// through it to bump counters.
//
// The vectors are split into three families:
//
//   - Lifecycle: instance create/destroy/trap counts, indexed by slug.
//   - Resource: fuel consumption, timeout occurrences.
//   - ABI: per-host-function call counts and latency, indexed by slug
//     and ABI name.
//
// Slug cardinality is bounded at registration time via RegisterSlug —
// callers MUST go through that path before emitting against a new
// slug. Direct .WithLabelValues calls on the underlying CounterVec
// would bypass the bound and silently grow the series count, which is
// exactly the cardinality explosion the bound prevents.
//
// PluginMetrics is safe for concurrent use.
type PluginMetrics struct {
	// fuelConsumed sums the fuel a plugin has burned across all calls.
	// Bumped from limits.Enforcer once a fuel meter lands (#15 follow-up);
	// today it is incremented at call-time from the per-call CPU clock
	// (best-effort proxy for fuel) so the metric is wired and useful
	// even before the explicit fuel counter ships.
	fuelConsumed *prometheus.CounterVec // labels: slug

	// timeoutTotal counts calls that exceeded the configured deadline.
	// abi label is the host function name when the timeout fired during
	// a guest-issued host call, or "_call" when the outer Module.Call
	// itself hit the soft/hard deadline.
	timeoutTotal *prometheus.CounterVec // labels: slug, abi

	// abiCallTotal counts every gn_* host call, split by status:
	// "ok", "error", "cardinality_exceeded", "trap". This is the
	// per-ABI traffic series — useful for spotting noisy plugins,
	// failing ABIs, and the cardinality-dam audit.
	abiCallTotal *prometheus.CounterVec // labels: slug, abi, status

	// instanceLifecycle counts module create/destroy/trap transitions.
	// One bump per LoadModule (event=create), one per Close (destroy),
	// one per classified TrapError surfaced through Module.Call (trap).
	instanceLifecycle *prometheus.CounterVec // labels: slug, event

	// abiCallDuration is the histogram of host-function latencies.
	// Same label set as abiCallTotal minus status (the histogram is
	// observed unconditionally; status lives in the counter).
	abiCallDuration *prometheus.HistogramVec // labels: slug, abi

	// metricCardinalityExceeded counts every gn_metric_observe call
	// that the cardinality dam dropped. Wired here for the #226
	// follow-up commit; today it has no producer.
	metricCardinalityExceeded *prometheus.CounterVec // labels: slug, metric

	// slugsMu guards the registered-slug set. RegisterSlug bumps the
	// set under the lock; the hot-path emission functions (Inc*) take
	// the read lock to verify the slug is approved.
	slugsMu sync.RWMutex
	slugs   map[string]struct{}
	limit   int
}

// NewPluginMetrics constructs the metric set, registers it against reg,
// and returns the populated struct. Panics on duplicate registration —
// the runtime is expected to wire exactly one PluginMetrics per
// process.
//
// Use NewPluginMetricsWithLimit for tests that want a different cap.
func NewPluginMetrics(reg prometheus.Registerer) *PluginMetrics {
	return NewPluginMetricsWithLimit(reg, MaxRegisteredPlugins)
}

// NewPluginMetricsWithLimit is the explicit-limit variant. limit <= 0
// falls back to MaxRegisteredPlugins.
func NewPluginMetricsWithLimit(reg prometheus.Registerer, limit int) *PluginMetrics {
	if reg == nil {
		// A nil registerer is a wiring bug. We could return a no-op
		// PluginMetrics, but the metrics are load-bearing for the
		// runtime's operability story — a silent no-op would mask the
		// misconfiguration in production.
		panic("runtime/metrics: NewPluginMetrics: registerer is required")
	}
	if limit <= 0 {
		limit = MaxRegisteredPlugins
	}

	pm := &PluginMetrics{
		slugs: make(map[string]struct{}),
		limit: limit,
	}

	pm.fuelConsumed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gonext_plugin_fuel_consumed_total",
		Help: "Cumulative fuel units consumed by each plugin across all host calls. Bound at MaxRegisteredPlugins slugs.",
	}, []string{"slug"})

	pm.timeoutTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gonext_plugin_timeout_total",
		Help: "Number of plugin calls that exceeded the configured CPU deadline, by slug and ABI.",
	}, []string{"slug", "abi"})

	pm.abiCallTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gonext_plugin_abi_call_total",
		Help: "Plugin host-function (gn_*) call counts, by slug, ABI name, and outcome status.",
	}, []string{"slug", "abi", "status"})

	pm.instanceLifecycle = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gonext_plugin_instance_lifecycle_total",
		Help: "Plugin instance lifecycle transitions, by slug and event (create|destroy|trap).",
	}, []string{"slug", "event"})

	pm.abiCallDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "gonext_plugin_abi_call_duration_seconds",
		Help: "Latency distribution of plugin host-function (gn_*) calls.",
		Buckets: []float64{
			0.000_010, 0.000_050, 0.000_100, 0.000_500,
			0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5,
		},
	}, []string{"slug", "abi"})

	pm.metricCardinalityExceeded = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gonext_plugin_metric_cardinality_exceeded_total",
		Help: "Number of gn_metric_observe calls dropped by the per-plugin cardinality dam, by slug and metric name.",
	}, []string{"slug", "metric"})

	reg.MustRegister(
		pm.fuelConsumed,
		pm.timeoutTotal,
		pm.abiCallTotal,
		pm.instanceLifecycle,
		pm.abiCallDuration,
		pm.metricCardinalityExceeded,
	)
	return pm
}

// RegisterSlug admits slug into the bounded-cardinality set. Subsequent
// emissions against slug succeed; emissions against unregistered slugs
// route to a well-known "_overflow" placeholder series so a runaway
// plugin can't grow the cardinality count.
//
// Returns *ErrPluginSlugLimit when the cap is hit. The caller — the
// Runtime's LoadModule path — surfaces this as a clear "too many
// plugins loaded" error to the operator.
func (pm *PluginMetrics) RegisterSlug(slug string) error {
	if pm == nil {
		return nil
	}
	if slug == "" {
		return fmt.Errorf("runtime/metrics: RegisterSlug: slug is required")
	}
	pm.slugsMu.Lock()
	defer pm.slugsMu.Unlock()
	if _, ok := pm.slugs[slug]; ok {
		return nil
	}
	if len(pm.slugs) >= pm.limit {
		return &ErrPluginSlugLimit{Slug: slug, Limit: pm.limit}
	}
	pm.slugs[slug] = struct{}{}
	return nil
}

// UnregisterSlug drops slug from the cardinality set. Existing series
// for that slug remain in Prometheus until the scrape rotates them out
// — Prometheus has no API to delete a single series — but subsequent
// emissions against the slug fall back to the cardinality-exceeded
// path. This keeps the slug accounting honest after a plugin uninstall.
//
// Idempotent: calling on an unknown slug is a no-op.
func (pm *PluginMetrics) UnregisterSlug(slug string) {
	if pm == nil || slug == "" {
		return
	}
	pm.slugsMu.Lock()
	defer pm.slugsMu.Unlock()
	delete(pm.slugs, slug)
}

// slugAdmitted reports whether slug has been admitted via RegisterSlug.
// Hot path — read-only, RLock-only, no allocation.
func (pm *PluginMetrics) slugAdmitted(slug string) bool {
	if pm == nil {
		return false
	}
	pm.slugsMu.RLock()
	defer pm.slugsMu.RUnlock()
	_, ok := pm.slugs[slug]
	return ok
}

// SlugCount returns the number of distinct slugs currently admitted.
// Exposed for tests and admin probes; do not use on the hot path.
func (pm *PluginMetrics) SlugCount() int {
	if pm == nil {
		return 0
	}
	pm.slugsMu.RLock()
	defer pm.slugsMu.RUnlock()
	return len(pm.slugs)
}

// IncFuel adds fuel units burned by slug. No-op when slug is not
// admitted — emission against an unregistered slug would grow the
// series count, defeating the cardinality bound.
func (pm *PluginMetrics) IncFuel(slug string, fuel float64) {
	if pm == nil || fuel <= 0 || !pm.slugAdmitted(slug) {
		return
	}
	pm.fuelConsumed.WithLabelValues(slug).Add(fuel)
}

// IncTimeout bumps the timeout counter for (slug, abi). abi is the
// host function the timeout fired during, or "_call" for the outer
// Module.Call envelope.
func (pm *PluginMetrics) IncTimeout(slug, abi string) {
	if pm == nil || !pm.slugAdmitted(slug) {
		return
	}
	if abi == "" {
		abi = "_call"
	}
	pm.timeoutTotal.WithLabelValues(slug, abi).Inc()
}

// IncABICall records a single host-function invocation. status is one
// of "ok", "error", "cardinality_exceeded", "trap" — adding new
// statuses is a documentation update, not an ABI break.
//
// An unregistered slug routes to a well-known "_overflow" placeholder
// series tagged status="cardinality_exceeded" so operators can see
// traffic from un-admitted plugins without paying the slug-distinct
// cardinality cost.
func (pm *PluginMetrics) IncABICall(slug, abi, status string) {
	if pm == nil {
		return
	}
	if status == "" {
		status = "ok"
	}
	if !pm.slugAdmitted(slug) {
		pm.abiCallTotal.WithLabelValues("_overflow", abi, "cardinality_exceeded").Inc()
		return
	}
	pm.abiCallTotal.WithLabelValues(slug, abi, status).Inc()
}

// ObserveABICallDuration records the latency of a host-function call.
// Skipped silently when slug is not admitted.
func (pm *PluginMetrics) ObserveABICallDuration(slug, abi string, seconds float64) {
	if pm == nil || !pm.slugAdmitted(slug) {
		return
	}
	pm.abiCallDuration.WithLabelValues(slug, abi).Observe(seconds)
}

// IncLifecycle records a create/destroy/trap transition. Valid events
// are "create", "destroy", "trap"; anything else is folded onto
// "unknown" so callers can't blow up cardinality with typos.
func (pm *PluginMetrics) IncLifecycle(slug, event string) {
	if pm == nil {
		return
	}
	switch event {
	case "create", "destroy", "trap":
	default:
		event = "unknown"
	}
	if !pm.slugAdmitted(slug) {
		pm.instanceLifecycle.WithLabelValues("_overflow", event).Inc()
		return
	}
	pm.instanceLifecycle.WithLabelValues(slug, event).Inc()
}

// IncMetricCardinalityExceeded records a drop from the per-plugin
// cardinality dam (gn_metric_observe). Wired here for the #226
// follow-up; today there is no producer.
func (pm *PluginMetrics) IncMetricCardinalityExceeded(slug, metric string) {
	if pm == nil {
		return
	}
	if metric == "" {
		metric = "_unset"
	}
	if !pm.slugAdmitted(slug) {
		pm.metricCardinalityExceeded.WithLabelValues("_overflow", metric).Inc()
		return
	}
	pm.metricCardinalityExceeded.WithLabelValues(slug, metric).Inc()
}
