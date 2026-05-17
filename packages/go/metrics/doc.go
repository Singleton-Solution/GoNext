// Package metrics is the GoNext Prometheus-metrics scaffold.
//
// It wraps prometheus/client_golang with three opinionated additions:
//
//   - A Registry that pre-registers the Go runtime and process collectors,
//     so every binary exposes the standard process_* and go_* series
//     without per-app glue.
//
//   - A dedicated metrics HTTP server (ServeMetrics) that binds /metrics
//     on a separate port from the public API listener. Scrape traffic
//     stays isolated from user traffic — a Prometheus deployment can hit
//     :9090 without reaching the public address, and operators can put
//     network policy in front of it. See docs/10-observability.md §5.1.
//
//   - Constructor helpers (NewCounter, NewHistogram, NewGauge) and shared
//     bucket sets (HTTPLatencyBuckets, DBLatencyBuckets, BytesBuckets)
//     so callers across packages produce comparable histograms. Buckets
//     mirror the catalog in docs/10-observability.md §5.1.
//
// Cardinality is the operational cost driver in Prometheus. The package
// ships MustBoundedLabels, a register-time guardrail that flags labels
// without a documented value bound. See docs/10-observability.md §5.4.
//
// Typical wiring in cmd/server/main.go:
//
//	reg := metrics.NewRegistry()
//	requestDuration := reg.NewHistogram(
//	    "gonext_http_request_duration_seconds",
//	    "HTTP request duration, by route template and method.",
//	    metrics.HTTPLatencyBuckets,
//	    "route", "method", "status_class",
//	)
//
//	metricsSrv, shutdown, err := reg.ServeMetrics(":9090", logger)
//	if err != nil { ... }
//	defer shutdown()
//	_ = metricsSrv
//
// See docs/10-observability.md §5 for the metric catalog and
// docs/13-security-baseline.md for why /metrics never shares the public
// listener.
package metrics
