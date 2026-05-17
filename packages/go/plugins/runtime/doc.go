// Package runtime is the WebAssembly host for GoNext plugins.
//
// Every plugin in GoNext ships as a WebAssembly module. The runtime
// package owns the hostbound half of that contract: it compiles the
// module's bytes, instantiates them inside an isolated wazero context,
// exposes a small set of host functions the guest can call back into
// (logging, panic, monotonic time), and lets host code invoke the
// module's exports.
//
// This is the FOUNDATION layer — issue #6 in the GoNext tracker. It
// deliberately stays small and unopinionated so the things that build
// on top of it can do so without fighting the abstractions:
//
//   - Instance pooling (issue #9) — the pool is a sidecar over Runtime
//     that holds N pre-instantiated Modules and hands them out to
//     callers. Nothing in this package precludes that; pools just call
//     LoadModule N times with the same bytes.
//
//   - Resource limits & fuel (issue #15) — the per-module hard caps
//     here (16 MiB memory) are placeholders. The real limit story uses
//     wazero's WithMemoryLimitPages + WithCloseOnContextDone + a fuel
//     meter the host injects. The Module type exposes its underlying
//     wazero handles via Module.WazeroModule so #15 can wire those
//     without refactoring this package.
//
//   - Capability ABI (issue #107) — the host functions registered in
//     host.go are the BARE MINIMUM the guest needs to be useful
//     (log, panic, time). The real capability surface (http.outbound,
//     kv.read/write, db.query, email.send, ...) is much larger and
//     lives in its own package once #107 lands. The capability host is
//     a HostBuilder seam: the Runtime accepts additional host modules
//     via WithHostModule, so the capability package can register its
//     own functions without modifying this package.
//
// # Threading model
//
// wazero is goroutine-safe at the runtime layer (multiple modules can
// be loaded concurrently) but a single api.Function.Call is NOT
// goroutine-safe — concurrent callers of the SAME exported function on
// the SAME module must serialize.
//
// The Module type in this package enforces that contract with a
// per-module sync.Mutex around every Call invocation. Concurrent Call
// from N goroutines on the same Module is therefore safe — it just
// serializes. Pool-based callers (#9) that want true parallelism keep
// N Modules and route calls round-robin.
//
// # Trap handling
//
// A WASM trap (host function panic, division by zero, out-of-bounds
// memory access, etc.) surfaces as a Go error from Module.Call. The
// runtime catches the trap, drains any partial stack, and returns a
// *TrapError wrapping the original cause. The module is left in its
// pre-trap state — wazero modules are not poisoned by traps unless the
// host explicitly closes them. Callers that want fail-fast semantics
// (drop the module after any trap) implement that policy themselves.
//
// gn_panic explicitly traps with a *TrapError carrying the guest's
// panic message decoded from linear memory.
//
// # Host function ABI
//
// Host functions live in the "env" namespace, matching the convention
// most WASM toolchains (Rust, AssemblyScript, TinyGo) emit imports
// under by default. The minimum surface registered here:
//
//	env.gn_log(level i32, ptr i32, len i32)
//	env.gn_panic(ptr i32, len i32)         // never returns
//	env.gn_time_ms() -> i64
//
// Strings are passed as (ptr, len) pairs into the guest's exported
// linear memory. The guest is expected to export a memory named
// "memory" (the universal default). If a guest doesn't export memory,
// calling a host function that reads memory returns a *HostError.
//
// # Typical wiring
//
//	rt, err := runtime.New(ctx,
//	    runtime.WithLogger(logger),
//	    runtime.WithTimeSource(time.Now),
//	)
//	if err != nil { return err }
//	defer rt.Close(ctx)
//
//	mod, err := rt.LoadModule(ctx, "blog-stats", wasmBytes)
//	if err != nil { return err }
//	defer mod.Close(ctx)
//
//	results, err := mod.Call(ctx, "on_activate")
//	if err != nil { ... }
//
// See packages/go/plugins/lifecycle for the state-machine half; the
// lifecycle Manager injects a Runtime adapter that wraps this package.
package runtime
