// Package bench implements the `gonext bench` subcommand: a small,
// in-tree synthetic load runner that exercises the same SLO buckets as
// the k6 scenarios in tools/load/k6/ but using only Go standard library
// HTTP. The point is to give contributors and CI smoke runs a quick way
// to verify the public hot paths without installing k6.
//
// Architecture:
//
//   - [Scenario] is a small interface implemented by files under
//     scenarios/. Each implementation owns one URL-shape mix and a
//     reference SLO bucket.
//   - [Runner] spins up a worker pool of VUs (virtual users) — plain
//     goroutines — that each call Scenario.Iter in a hot loop. A ramp
//     schedule controls when each VU becomes active.
//   - Per-request samples (RTT, status, error) flow into the runner's
//     [Aggregator] which produces a [Report] when the run ends.
//   - [Report] computes p50/p95/p99 from the recorded samples, prints
//     the table or JSON, and is fed to slo.go which compares it to the
//     bucket each scenario advertises.
//
// This subcommand intentionally avoids exotic dependencies; it is the
// "k6 isn't installed" fallback. The k6 scripts at tools/load/k6/ are
// the authoritative load harness — `gonext bench` mirrors their shape
// so a contributor who passes here is in the right ballpark.
package bench
