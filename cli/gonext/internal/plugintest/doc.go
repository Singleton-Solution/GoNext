// Package plugintest implements the plugin contract checks driven by the
// `gonext plugin test` CLI subcommand.
//
// The plugin contract — as described in docs/11-testing-ci.md §7.1 — has
// eight checks. Some require the WASM host (wazero-based runtime) which has
// not yet landed in the tree. This package implements the checks that can
// run today against a bundle on disk:
//
//   - Bundle layout: the input is either an unpacked directory or a
//     `.gnplugin` zip archive with the required entries (manifest, WASM).
//   - Manifest schema: the manifest is JSON, validates against the v1
//     schema dialect (JSON Schema 2020-12 per docs/02-plugin-system.md §7.7),
//     declares the required fields and an `abi_version` the host supports,
//     and only references capabilities from the published vocabulary.
//   - WASM module readability: the embedded `server/plugin.wasm` (or the
//     `wasm` path declared in the manifest) parses as a WebAssembly module
//     to at least the level of a magic-number + version check. The full
//     instantiation-under-fuel-caps check is gated on the host landing.
//
// The remaining checks (hook registration, capability dispatch, migration
// round-trip, hash determinism, sample-dispatch budget) are scaffolded as
// `Reserved` checks that report `skipped — runtime not yet available`. They
// keep their stable check names so the JSON report shape doesn't churn when
// the host lands.
//
// Output is a [Report] whose JSON shape is stable across check additions —
// new checks append rows, never reorder. The marketplace ingest pipeline
// described in docs/11-testing-ci.md §7.2 reads this shape directly.
package plugintest
