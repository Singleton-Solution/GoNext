// Package pool keeps a set of pre-instantiated wazero modules ready
// for short, per-request plugin calls.
//
// Creating a fresh wazero module is expensive — roughly 1–5 ms for a
// trivially small plugin, more for anything realistic — because the
// guest's data segments, tables, globals, and host-imports all have
// to be wired before the first export can be invoked. That's fine for
// long-lived plugin instances (a background worker, a startup hook),
// but it is far too much overhead for the hot path that issue #9 calls
// out: filter hooks and REST handlers that run on every request.
//
// The pool is the answer. One Pool wraps a single set of WASM bytes
// and the runtime.Runtime that compiled them, holds N pre-warmed
// runtime.Module instances, and hands them out via Checkout. The
// caller uses the module for the duration of one request, then calls
// Lease.Return() to make the instance available again.
//
// # Lifecycle
//
//   - Pool.Start instantiates MinInstances modules up-front so the
//     first checkout is hot.
//   - Checkout returns a Lease wrapping a module. It blocks (up to
//     ctx.Deadline) when every instance is in use and the pool is at
//     MaxInstances.
//   - Lease.Return puts the instance back. If MaxUsesPerInstance has
//     been hit, the pool transparently recycles (closes the old one,
//     creates a fresh one) so the next checkout sees a clean module.
//   - The reaper goroutine wakes periodically and closes any instance
//     idle longer than MaxIdleTime, refilling to MinInstances if the
//     close took the pool below the floor.
//   - Pool.Close drains every checked-out lease and tears everything
//     down. Once Close returns, the pool will not allocate further
//     resources.
//
// # Trap handling
//
// When a plugin call traps (panic, OOB memory, runaway loop killed by
// ctx), the caller marks the lease unusable via Lease.MarkUnusable
// before returning it. The pool closes the trapped instance on
// Return rather than putting it back into rotation — the guest's
// internal state may be poisoned after a trap.
//
// # Metrics
//
// The package exports a Metrics struct that wraps Prometheus counters
// for checkout volume, checkout wait, recycle reasons, and the live
// pool size. metrics.go documents the names; everyday callers wire it
// via NewMetrics(reg).
//
// # Concurrency
//
// Pool is goroutine-safe. Checkout, Return, Close, and the reaper all
// coordinate via a single sync.Mutex around the idle slice plus a
// condition variable for blocked-checkout wakeups. Hot-path metrics
// use lock-free atomic ops.
package pool
