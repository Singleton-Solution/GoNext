package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	gonextmetrics "github.com/Singleton-Solution/GoNext/packages/go/metrics"
)

// Metric names. Exported so tests and dashboards can reference them by
// constant rather than re-stringifying. Changing any of these is a
// breaking change for dashboards and alert rules — add a new metric
// instead of renaming.
const (
	// MetricRequestsTotal is the counter name.
	MetricRequestsTotal = "gonext_http_requests_total"

	// MetricRequestDurationSeconds is the histogram name.
	MetricRequestDurationSeconds = "gonext_http_request_duration_seconds"

	// MetricInflightRequests is the gauge name.
	MetricInflightRequests = "gonext_http_inflight_requests"
)

// Label names. Kept as constants so a typo can't drift the schema across
// the three families.
const (
	labelMethod = "method"
	labelRoute  = "route"
	labelStatus = "status"
)

// collectors bundles the three families the middleware observes. It is
// constructed once per Middleware call and shared across every request
// the returned handler serves.
//
// The bundle is intentionally NOT exported: callers shouldn't poke at
// the underlying CounterVec / HistogramVec / GaugeVec directly. If a
// caller needs custom labels, they should add their own metrics in
// their own package — this middleware owns the canonical http_* family.
type collectors struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	inflight        *prometheus.GaugeVec
}

// newCollectors registers the three families on reg and returns the
// bundle. Panics on duplicate registration, matching the convention of
// prometheus.MustRegister and metrics.Registry.NewCounter — a metric
// name collision is a programming error worth crashing for.
//
// reg may be nil; in that case prometheus.DefaultRegisterer is used.
// In practice every binary passes its own *prometheus.Registry from
// metrics.Registry.Prometheus() so /metrics is isolated from any
// global state.
func newCollectors(reg prometheus.Registerer) *collectors {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: MetricRequestsTotal,
			Help: "Total HTTP requests served, by method, route template, and response status code.",
		},
		[]string{labelMethod, labelRoute, labelStatus},
	)

	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    MetricRequestDurationSeconds,
			Help:    "HTTP request duration in seconds, by method and route template.",
			Buckets: gonextmetrics.HTTPLatencyBuckets,
		},
		[]string{labelMethod, labelRoute},
	)

	inflight := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: MetricInflightRequests,
			Help: "Current number of HTTP requests in flight, by method and route template.",
		},
		[]string{labelMethod, labelRoute},
	)

	reg.MustRegister(requestsTotal, requestDuration, inflight)

	return &collectors{
		requestsTotal:   requestsTotal,
		requestDuration: requestDuration,
		inflight:        inflight,
	}
}
