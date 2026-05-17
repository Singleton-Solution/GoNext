package metrics

// Shared histogram bucket sets. Using the same buckets across the codebase
// keeps histograms comparable in Grafana and lets operators reason about
// SLOs without per-metric overrides.
//
// The values mirror docs/10-observability.md §5.1: exponential-ish ranges
// chosen for the signal's natural distribution. Once shipped, changing
// buckets is a breaking change for dashboards and alert thresholds — add
// a new bucket set rather than mutating an existing one.

// HTTPLatencyBuckets covers typical HTTP request durations from sub-ms
// healthchecks up to 10s timeouts. Suitable for gonext_http_request_duration_seconds
// and related histograms.
var HTTPLatencyBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// DBLatencyBuckets covers query/transaction durations. Same range as
// HTTP — most queries are fast, slow-query alerts live in the upper tail.
var DBLatencyBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// BytesBuckets covers payload sizes for request/response bodies, cache
// values, upload chunks. Ranges from 64B to 64MB in roughly-1024×
// powers-of-two steps; tails outside that range are usually pathological.
var BytesBuckets = []float64{
	64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864,
}
