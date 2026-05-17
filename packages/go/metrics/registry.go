package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Registry wraps a *prometheus.Registry with the GoNext defaults already
// applied: Go runtime collector and process collector are pre-registered,
// so every binary exposes go_* and process_* series without per-app glue.
//
// Registry is safe for concurrent use; the underlying prometheus.Registry
// serializes its own state.
type Registry struct {
	reg *prometheus.Registry
}

// NewRegistry returns a Registry seeded with the standard runtime and
// process collectors. Subsequent NewCounter / NewHistogram / NewGauge
// calls register against this Registry.
//
// Callers wanting to merge with the global default registry can use
// NewRegistryFrom(prometheus.DefaultRegisterer.(*prometheus.Registry)),
// but the package recommends an isolated registry per binary so test
// state does not leak between tests.
func NewRegistry() *Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &Registry{reg: reg}
}

// NewRegistryFrom wraps an existing *prometheus.Registry without seeding
// any default collectors. Intended for callers that already manage their
// own Registry lifecycle (rare; prefer NewRegistry).
func NewRegistryFrom(reg *prometheus.Registry) *Registry {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}
	return &Registry{reg: reg}
}

// Prometheus returns the underlying *prometheus.Registry. Callers that
// need to register custom collectors (e.g. a sql.DBStats collector) use
// this; everyday code should prefer the typed helpers below.
func (r *Registry) Prometheus() *prometheus.Registry {
	return r.reg
}

// NewCounter registers a new *prometheus.CounterVec with the registry.
//
// name is the fully-qualified metric name (e.g. "gonext_http_requests_total").
// Use the gonext_ prefix; see docs/10-observability.md §5.3.
//
// Panics on duplicate registration — this is intentional: a programming
// error caught at startup is cheaper than mismatched metric series in
// production.
func (r *Registry) NewCounter(name, help string, labels ...string) *prometheus.CounterVec {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: name,
		Help: help,
	}, labels)
	r.reg.MustRegister(v)
	return v
}

// NewHistogram registers a new *prometheus.HistogramVec. buckets must be
// sorted ascending; pass one of HTTPLatencyBuckets, DBLatencyBuckets,
// BytesBuckets, or a domain-specific set.
//
// If buckets is nil or empty, the prometheus.DefBuckets default is used
// — but every histogram in the catalog should pick a deliberate bucket
// set, so callers shouldn't rely on the default.
func (r *Registry) NewHistogram(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	opts := prometheus.HistogramOpts{
		Name: name,
		Help: help,
	}
	if len(buckets) > 0 {
		opts.Buckets = buckets
	}
	v := prometheus.NewHistogramVec(opts, labels)
	r.reg.MustRegister(v)
	return v
}

// NewGauge registers a new *prometheus.GaugeVec.
func (r *Registry) NewGauge(name, help string, labels ...string) *prometheus.GaugeVec {
	v := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	}, labels)
	r.reg.MustRegister(v)
	return v
}

// MustRegister forwards to the underlying registry's MustRegister for
// callers that build collectors directly (e.g. a custom collector from
// a connection pool's Stats()).
func (r *Registry) MustRegister(cs ...prometheus.Collector) {
	r.reg.MustRegister(cs...)
}

// Register forwards to the underlying registry's Register, returning the
// error rather than panicking. Use this when duplicate registration is
// acceptable (e.g. plugin reload) and the caller wants to inspect the
// AlreadyRegisteredError.
func (r *Registry) Register(c prometheus.Collector) error {
	if err := r.reg.Register(c); err != nil {
		return fmt.Errorf("metrics.Registry.Register: %w", err)
	}
	return nil
}
