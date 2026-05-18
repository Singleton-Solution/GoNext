// Package metrics is the GoNext HTTP-server metrics middleware. It wires
// the canonical http_* metric family (request count, in-flight gauge,
// latency histogram) into the standard middleware chain used by binaries
// that serve HTTP traffic (apps/api today).
//
// # Metric catalog
//
// Three families are registered against the *prometheus.Registerer
// passed to Middleware:
//
//   - gonext_http_requests_total{method, route, status}
//     CounterVec. Incremented once per request after the response is
//     written, with the captured status code stringified.
//
//   - gonext_http_request_duration_seconds{method, route}
//     HistogramVec using metrics.HTTPLatencyBuckets. Observed once per
//     request with the time elapsed between handler entry and response
//     completion.
//
//   - gonext_http_inflight_requests{method, route}
//     GaugeVec. Incremented on handler entry, decremented on exit via
//     defer so panics don't leak the gauge.
//
// All three families share the same {method, route} label pair (status
// is added to the counter only). Status is intentionally NOT a label on
// the histogram or gauge — adding it would multiply cardinality without
// changing the operational picture for latency / saturation alerting.
//
// # Route label and cardinality
//
// The route label is the route TEMPLATE (e.g. "/users/{id}"), not the
// raw URL path. On Go 1.22+ std-mux, http.Request.Pattern carries the
// matched pattern after routing — this middleware reads it. When the
// pattern is empty (request didn't match any route, or middleware ran
// before routing), the label falls back to "unknown" rather than the
// raw path. A 1000-RPS scan over /aaaa, /bbbb, … therefore produces ONE
// "unknown" series, not 1000.
//
// See docs/10-observability.md §5.4 (cardinality budget) for why this
// matters: an unbounded route label is the most common way to blow up
// a Prometheus deployment.
//
// # Wiring
//
// In a typical handler chain (outermost first):
//
//	reg := metrics.NewRegistry()
//	httpMetrics := httpmetrics.Middleware(reg.Prometheus())
//
//	httpx.Chain(handler,
//	    httpx.Recovery(logger),
//	    httpx.RequestID(),
//	    httpx.Logger(logger),
//	    httpMetrics,
//	)
//
// Place AFTER Recovery (so panics still get recorded as 500s in the
// counter via the deferred observation) and AFTER RequestID/Logger
// (so log lines emitted by the handler still get a request_id). The
// histogram observation happens on the way out of the chain, so the
// timer encompasses everything BELOW this middleware — which is what
// you want for "how long does this handler actually take".
//
// # Status capture
//
// The middleware wraps ResponseWriter to capture the status code. The
// wrapper implements http.Flusher and exposes Unwrap() so
// http.ResponseController (Hijack, Push, etc.) works through it. If
// the handler never calls WriteHeader, the captured status defaults to
// 200, matching net/http semantics.
package metrics
