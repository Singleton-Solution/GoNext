// Package health is the plugin-health dashboard surface.
//
// It joins three signals — Prometheus metrics, an in-memory ring buffer
// of recent traps, and the running set of capability denials — into a
// single per-plugin Report that the admin UI consumes via a REST
// handler. The package owns no plugin lifecycle and never calls a
// plugin: it only observes what the runtime and the capability checker
// report into a Recorder.
//
// # Why a dedicated package
//
// The runtime, the pool, the hook ABI bridge, and the capability
// checker all need to emit plugin telemetry, but none of them should
// own the Prometheus registration or the ring-buffer cache — that
// would either (a) duplicate state across packages or (b) create
// circular imports between runtime and metrics. The Recorder
// interface lets each producer call a single observer without
// importing this package's concrete types; only main.go (or
// equivalent wiring) constructs the concrete Recorder and threads it
// through.
//
// # The four metrics
//
// Following the catalog in docs/10-observability.md §5.5:
//
//   - gonext_plugin_invocations_total{plugin, hook, result}
//     counter, one per hook dispatch. "result" is one of the
//     ResultStatus names ("ok", "error", "trap", ...).
//
//   - gonext_plugin_duration_seconds{plugin, hook}
//     histogram, observed on every dispatch regardless of result.
//
//   - gonext_plugin_traps_total{plugin, reason}
//     counter, one per trap. "reason" is a short token derived from
//     the TrapError.Reason (lowercased, spaces -> underscores) so the
//     cardinality stays bounded.
//
//   - gonext_plugin_capability_denied_total{plugin, capability}
//     counter, one per capabilities.MustAllow denial.
//
// # The ring buffer
//
// The metrics above are the right shape for Grafana and alerting, but
// they answer aggregate questions ("how often is plugin X trapping?")
// rather than the operator's first reflex on a fresh page: "what
// just broke?". The Recorder therefore also keeps the last RingSize
// traps per plugin in memory, addressable by a stable TrapEvent.ID
// that the admin UI uses to drill into a single failure and (via
// the dev-mode `gonext plugin replay` CLI) re-run the same
// invocation against the currently-loaded plugin bytes.
//
// Ring entries are NOT durable. A process restart clears them; an
// operator who wants long-term retention can scrape the
// gonext_plugin_traps_total counter or query the audit log (the
// capability checker emits capability.denied audit rows on its own).
//
// # Report shape
//
// Report{Plugin, Invocations, Errors, Latency: {P50, P95, P99},
// RecentTraps []TrapEvent} is the JSON shape returned by the HTTP
// handler. Percentiles are computed in-process from the same
// histogram the metrics endpoint exposes — operators get the same
// view regardless of which side they query.
//
// # Wiring
//
// Typical main.go wiring:
//
//	rec := health.NewRecorder(metricsReg)
//	dispatcher := hooks.NewDispatcher(mod, hooks.WithRecorder(rec, pluginSlug))
//	checker := capabilities.NewChecker(reg, granted,
//	    capabilities.WithDenialRecorder(rec, pluginSlug))
//	mux.Handle("/api/v1/plugins/", health.NewHandler(rec))
package health
