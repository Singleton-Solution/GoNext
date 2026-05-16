# ADR 0005: Plugins run as WebAssembly modules hosted via wazero

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 00 §3.1 (the "three hard problems"), doc 02 (full plugin system design)
- **Informed**: plugin SDK authors, security reviewers

## Context

The single biggest reason serious users leave WordPress is the plugin security story. The WP plugin model gives third-party code unrestricted PHP execution: arbitrary `eval()`, raw DB connections, full filesystem access, ability to override core functions via monkey-patching, ability to schedule arbitrary cron with arbitrary code. The result is a constant stream of CVEs, supply-chain compromises (compromised plugin updates), and lateral movement from a single bad plugin to the entire site. This is the documented gap that our positioning (proposal S1) targets.

A modern plugin runtime has to solve simultaneously: **isolation** (a malicious or buggy plugin cannot corrupt other plugins or the host), **resource limits** (a plugin cannot exhaust memory, CPU, or DB connections), **capability scoping** (a plugin can only do what its manifest declares — covered by ADR 0012), **language flexibility** (authors should not be forced into one language), **deployment simplicity** (the host stays a single static binary), and **performance** (hooks are called millions of times — they cannot be slow).

The candidates:

- **Native Go plugins (`plugin` package).** Loads `.so` files at runtime. Linux-only, no isolation (a plugin runs in the host's address space with full Go runtime access), no resource limits, ABI is famously fragile across Go versions. Non-starter.
- **Hashicorp `go-plugin` (gRPC subprocess).** Each plugin is a separate OS process the host talks to over gRPC. Works, mature, used in Terraform and Vault. Heavy: a process per plugin, IPC overhead on every hook, complicated lifecycle and crash recovery. Resource limits via cgroups (Linux-only at decent fidelity).
- **Embedded JavaScript (V8, QuickJS, Goja).** Limits authors to JS. Hard to enforce CPU limits at the engine level (V8 has weak knobs here; QuickJS has better ones but no JIT). Memory limits are doable. The JS-only constraint loses Rust authors and the WASM-from-anywhere flexibility.
- **PHP compatibility layer.** Defeats the purpose. Doc 00 §1 calls this out as a v1 non-goal.
- **WebAssembly via wazero.** A WASM runtime in pure Go (no CGO). Plugins compile from Go, Rust, AssemblyScript, TypeScript (via Javy), C/C++, Zig — any language with a WASM target. Memory is isolated per module instance. Resource limits (memory, fuel, wall-clock) are first-class. No filesystem or network access except through host-imported functions. The host stays a single static cross-compilable Go binary.

The performance question is the genuine concern. wazero does not produce native machine code at the Wasmtime/Cranelift level — it has an optimizing compiler engine that is competitive but not state-of-the-art. For hot hooks (`the_content`, called once per render), the per-invocation overhead has to be acceptable. Doc 02 §4.6 measures cold start at 5–20ms and warm dispatch at 50–500µs of host overhead, which is acceptable for our workload. If a plugin needs to do heavyweight number-crunching, the host ABI escape hatch lets it call a Go-backed host function (doc 02 §4.1).

The component model / WIT story is not yet stable across the toolchain ecosystem. Doc 02 §4.3 commits to raw imports for ABI v1 with a plan to layer WIT on top once toolchains and runtimes converge — that migration changes SDK shape but not the underlying runtime.

## Decision

Plugins are WebAssembly modules executed by the Go-native `wazero` runtime, embedded in the host binary. Each plugin is one `.wasm` module loaded at activation, compiled once (`wazero.CompiledModule`), and instantiated as needed into a per-plugin pool. The host exposes a stable capability-gated ABI (ADR 0012) via raw host imports. Plugins author against per-language SDKs; v1 ships Rust and TypeScript SDKs as canonical (proposal Q00-2), with the ABI documented so third parties can build any other.

## Consequences

### Positive

- **Real isolation.** A plugin cannot read another plugin's memory, cannot touch the filesystem, cannot open arbitrary network sockets, cannot call into core Go code outside the declared ABI. The list of "what a plugin cannot do" (doc 02 §6.8) is the threat model, not an aspiration.
- **Real resource limits.** Memory caps via wazero `MemoryLimitPages`; instruction-counting "fuel" via context cancellation; wall-clock timeouts on every invocation; per-invocation HTTP/DB/KV quotas (doc 02 §4.5). A runaway plugin trips a circuit-breaker (doc 02 §4.5) and gets auto-deactivated after repeated failures.
- **Language flexibility.** Rust for serious authors, TypeScript via Javy for the WordPress-demographic majority, anything-that-compiles-to-WASM for everyone else. The ABI is the contract; SDKs are sugar.
- **Single-binary deploy.** wazero is pure Go. No CGO means no dynamic libraries, no cross-compile headaches, no debug-symbol mismatches. The deploy story stays one static binary plus Postgres plus Redis.
- **Compile-once, instantiate-many.** `CompiledModule` is built at install (5-20ms for small plugins, multi-second for large ones); instances are cheap. Plugins active across many sites in a future multisite (Q02-12) share the compiled module.

### Negative

- **No native code JIT today.** Plugins that do heavy CPU work in the guest will be slower than equivalent native code. Mitigation: the host ABI lets a plugin call out to a Go-backed host function for heavy primitives; we benchmark hot hooks and document the perf budget.
- **WIT / Component Model is deferred.** ABI v1 is raw imports with MessagePack on the wire. When the ecosystem converges on WIT, we layer it on top — SDKs change, plugins compiled against ABI v1 keep working as long as the host supports v1 (doc 02 §4.3, proposal Q02-9).
- **WASM toolchain maturity varies by language.** Rust is excellent. TypeScript via Javy is good but not as small or fast. Go-to-WASM is awkward (large binaries from TinyGo's footprint, runtime caveats). We pick Rust + TS as first-party SDKs deliberately to avoid promising Go support before TinyGo gets smaller.
- **Debugging WASM is harder than debugging native code.** Stack traces in the host show "trap in module X at offset Y," not a source line. Doc 02 §11.2 commits to source-map plumbing in the SDKs to recover decent stack traces.

### Neutral / accepted tradeoffs

- Per-plugin instance pools (doc 02 §4.6) use more memory than a "single shared instance" model would, but the isolation benefit is the whole point. We do not link all plugins into one module (doc 02 §4.7).
- The plugin SDK ships separately from core under a permissive license (Apache 2.0 per ADR 0001), so plugin authors are not encumbered by FSL.

## Alternatives considered

### Option A: Native Go plugins (`plugin` pkg)
- Rejected. Linux-only, no isolation, ABI fragile across Go versions, and a panic in a plugin crashes the host. Disqualifying on every axis.

### Option B: Hashicorp `go-plugin` (gRPC subprocess)
- Rejected. Heavy: one OS process per plugin, IPC on every hook. Lifecycle complexity (crash recovery, zombie processes). Resource limits require cgroups, which limits to Linux. Doc 02 §13.2 walks through the rejection in detail.

### Option C: Embedded JavaScript (V8, QuickJS, Goja)
- Rejected. Limits authors to JS — loses Rust and the polyglot story that distinguishes us from existing JS-first CMSes (Strapi, Payload). CPU limits are harder to enforce cleanly in JS engines than in WASM via fuel.

### Option D: PHP compatibility layer
- Rejected. Massive scope, defeats the project's purpose. The migration story is content-import, not code-execution.

### Option E: Wasmtime (CGO)
- Rejected. Wasmtime is faster than wazero today on raw throughput, but pulling in CGO destroys the single-binary cross-compile story. The host wins more from being a static binary than from a 1.5× faster guest. Worth revisiting only if measured workloads make wazero a real bottleneck.

### Option F: A custom interpreter / bytecode VM
- Rejected. Reinvents WebAssembly badly.

## References

- Design doc: `docs/00-architecture-overview.md` §3.1 (the three hard problems)
- Design doc: `docs/02-plugin-system.md` §4 (WASM runtime), §6 (host ABI), §13 (trade-offs)
- Proposal: `docs/proposals/14-proposals-foundation.md` Q00-2 (SDK languages)
- wazero: https://github.com/tetratelabs/wazero
- Related ADRs: ADR 0012 (capability ABI), ADR 0006 (monorepo houses SDKs)
