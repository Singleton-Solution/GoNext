package asynq

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metric names. The gonext_jobs_ prefix follows the project's metric
// naming convention (docs/10-observability.md §5.3). Counter names end in
// _total per the Prometheus convention; gauge names do not.
//
// These names are part of our public observability contract — they're
// referenced by dashboards, alert rules, and SLO docs. Renaming any of
// them is a breaking change.
const (
	metricProcessed = "gonext_jobs_processed_total"
	metricFailed    = "gonext_jobs_failed_total"
	metricInflight  = "gonext_jobs_inflight"
	metricUnknown   = "gonext_jobs_unknown_total"
)

// metrics bundles the four Prometheus collectors emitted by the chassis.
// One instance per server; collectors are owned by the supplied
// Registerer and stay alive for its lifetime.
//
// All series are labeled by queue. We deliberately do NOT add a "task
// type" label — task types are an unbounded set (every handler defines
// its own string) and would blow the cardinality budget. Per-task-type
// breakdowns belong in tracing, not Prometheus.
type metrics struct {
	processed *prometheus.CounterVec
	failed    *prometheus.CounterVec
	inflight  *prometheus.GaugeVec
	unknown   *prometheus.CounterVec
}

// newMetrics registers the four collectors against reg. Passing a nil
// Registerer is supported (returns a metrics struct whose methods are
// no-ops) so unit tests that don't care about Prometheus don't have to
// plumb a registry through.
//
// Registration uses prometheus.NewCounterVec + reg.MustRegister rather
// than going through packages/go/metrics.Registry's typed helpers — we
// want this package to be usable by tests that build their own raw
// prometheus.Registry, and we don't want to drag the whole metrics
// package in for two lines of collector wiring.
func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		processed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricProcessed,
			Help: "Total number of tasks successfully processed, by queue.",
		}, []string{"queue"}),
		failed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricFailed,
			Help: "Total number of tasks whose handler returned an error, by queue.",
		}, []string{"queue"}),
		inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricInflight,
			Help: "Number of tasks currently executing on this server, by queue.",
		}, []string{"queue"}),
		unknown: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricUnknown,
			Help: "Total number of tasks routed to the NotFound handler (unknown task type), by queue.",
		}, []string{"queue"}),
	}
	if reg == nil {
		return m
	}
	reg.MustRegister(m.processed, m.failed, m.inflight, m.unknown)
	return m
}

// observeStart bumps the inflight gauge as a handler begins. Always
// paired with observeFinish in a defer. The queue label is one of the
// seven canonical queue names; we don't sanity-check that here because
// the value comes from Asynq itself.
func (m *metrics) observeStart(queue string) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(queue).Inc()
}

// observeFinish records a handler outcome. err == nil increments
// processed; non-nil increments failed. Always decrements inflight,
// regardless of outcome — the symmetric pairing with observeStart is
// what keeps the gauge accurate even under panic recovery.
func (m *metrics) observeFinish(queue string, err error) {
	if m == nil {
		return
	}
	m.inflight.WithLabelValues(queue).Dec()
	if err != nil {
		m.failed.WithLabelValues(queue).Inc()
		return
	}
	m.processed.WithLabelValues(queue).Inc()
}

// observeUnknown increments the unknown counter without changing
// processed/failed. This separates "we ran a real handler and it
// failed" from "we got a task we don't know how to run", which lets
// operators detect deploy/code skew without it showing up as a generic
// error spike.
//
// Asynq still counts the NACK as a failure internally (the task
// retries, then archives), so unknown is additive context rather than
// a substitute for the failed metric.
func (m *metrics) observeUnknown(queue string) {
	if m == nil {
		return
	}
	m.unknown.WithLabelValues(queue).Inc()
}
