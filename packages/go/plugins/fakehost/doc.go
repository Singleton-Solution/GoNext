// Package fakehost is an in-memory implementation of the GoNext plugin
// host ABIs ([gn_db_read], [gn_kv_*], [gn_cache_invalidate],
// [gn_http_fetch], [gn_media_read], [gn_users_read], [gn_secrets_get],
// [gn_audit_emit], [gn_cron_register], [gn_log], [gn_time_ms],
// [gn_i18n_translate], [gn_metric_observe], [gn_event_emit],
// [gn_span_event]).
//
// It exists to drive the `gonext plugin test --suite=conformance` command
// (issue #247): plugin authors can run their plugin against a fake host
// offline, without standing up Postgres / Redis / the wazero runtime, and
// assert on the typed events the plugin emits.
//
// # Why not use the real host?
//
// The real wazero-backed [github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime.Runtime]
// is the right thing to use for end-to-end testing but its setup cost is
// prohibitive for a per-plugin author's inner loop:
//
//   - It needs a live Postgres pool for [gn_db_read]/[gn_db_write].
//   - It needs a live Redis for [gn_kv_*] and [gn_cache_invalidate].
//   - It instantiates a wazero runtime per call, which dominates the wall
//     clock for sub-millisecond hooks.
//
// The fake host replaces all of that with maps and a fixed clock. Plugin
// authors interact with the fake host the same way the real host interacts
// with their plugin: through typed function calls representing each ABI
// surface. Every call is recorded so a scenario can assert "the plugin
// called [gn_kv_set] with key=X value=Y in this order".
//
// # Scope
//
// This package is intentionally NOT a wazero-compatible host module — it
// does not register `Export("gn_*")` functions onto a wazero runtime.
// Connecting fakehost to the wazero ABI is the responsibility of the
// integration layer (the conformance runner in
// [github.com/Singleton-Solution/GoNext/packages/go/plugins/conformance]
// invokes fakehost methods directly from scenarios).
//
// What fakehost provides is the BEHAVIOUR plugin authors need to simulate:
// a recording in-memory KV, a recording mock audit emitter, a recording
// HTTP client whose responses are scripted, etc.
//
// # Goroutine safety
//
// A single [Host] is safe for concurrent use. The internal state (KV map,
// recorded events) is guarded by a mutex; plugin scenarios that fan out
// goroutines see a serialised view of recorded events.
package fakehost
