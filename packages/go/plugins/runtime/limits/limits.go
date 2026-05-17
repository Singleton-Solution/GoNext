// Package limits defines and enforces per-plugin resource caps for the
// wazero-backed plugin runtime (#15).
//
// The runtime (PR #350) already caps linear memory at module
// instantiation. This package layers two further controls on top:
//
//  1. CPU-time enforcement, expressed as a *soft* and *hard* timeout
//     attached to each Module.Call. The soft deadline lets the guest
//     observe ctx.Done() via wazero's WithCloseOnContextDone handling
//     and unwind cleanly; the hard deadline force-aborts the call if
//     the guest is wedged in a tight loop and never returns.
//  2. Per-plugin instance count, surfaced as a counter the module pool
//     (#9) consults before checking out another instance.
//
// The numbers in Default() are conservative because plugins are
// untrusted code. Hosts that have established trust for a particular
// plugin can raise them via the manifest layer (manifest fields land in
// a follow-up).
//
// This package is intentionally free of wazero imports — that lives in
// the runtime package, which translates Limits into wazero
// configuration. Keeping limits.go provider-agnostic means a future
// non-wazero backend (e.g., wasmtime, wasm-micro-runtime) could share
// the same Limits / Enforcer types.
package limits

import (
	"fmt"
	"time"
)

// Per-plugin defaults. Tuned for the workloads we expect during initial
// rollout: content transforms, validation hooks, lifecycle webhooks.
// All four are "safe to live with for months" — they will not surprise
// a developer running a perfectly-behaved plugin, but they will
// strangle a runaway plugin before it can swamp the host.
const (
	// DefaultMemoryPages caps linear memory at 256 pages (16 MiB).
	// Matches the existing wazero-level cap in runtime.go so the two
	// stay in lockstep.
	DefaultMemoryPages uint32 = 256

	// DefaultCPUTimeoutSoft is the soft deadline for a single
	// Module.Call. After this elapses the call ctx is canceled; wazero
	// surfaces the cancellation as a trap so a well-behaved guest can
	// unwind (defer-style) before being killed.
	//
	// 2s is generous for the kind of work plugins do. A guest that
	// can't finish in 2s is almost certainly stuck.
	DefaultCPUTimeoutSoft = 2 * time.Second

	// DefaultCPUTimeoutHard is the hard kill deadline. If the guest is
	// still running this long after the soft signal — most commonly
	// because it's caught in a tight loop with no host calls to
	// observe ctx.Done() — the enforcer treats it as wedged and the
	// hard cancel fires.
	//
	// 5s total budget gives 3s headroom over the soft deadline for the
	// guest to actually notice it should stop. That's more than enough
	// for any real cleanup path.
	DefaultCPUTimeoutHard = 5 * time.Second

	// DefaultMaxInstancesPerPlugin caps the number of concurrently-live
	// instances of a single plugin. The pool (#9) consults this when
	// checking out a fresh instance to keep one plugin from
	// monopolising memory by spawning copies.
	//
	// 16 is a sensible default for a single-machine host serving a
	// few hundred RPS — beyond that the pool ought to be the
	// bottleneck, not us.
	DefaultMaxInstancesPerPlugin = 16
)

// Limits is the per-plugin resource envelope.
//
// All fields are zero-friendly: a zero value disables that particular
// limit (e.g. CPUTimeoutSoft == 0 means "no soft deadline"), with the
// sole exception of MemoryPages — zero pages would refuse every
// instantiation, which is never what the caller wants, so the runtime
// substitutes DefaultMemoryPages when it sees zero.
//
// Limits is a value type: pass it by value, mutate copies freely. The
// runtime stores its own private copy.
type Limits struct {
	// MemoryPages is the hard cap on the module's linear memory, in
	// 64 KiB wazero pages. 256 = 16 MiB.
	MemoryPages uint32

	// CPUTimeoutSoft is the wall-clock budget for a single call. When
	// it elapses, the enforcer cancels the call context. A guest that
	// honors ctx.Done() (via host-function returns or
	// WithCloseOnContextDone) can unwind cleanly.
	//
	// Zero disables the soft deadline. In practice you should always
	// set this — a guest with no deadline can hang the calling
	// goroutine indefinitely.
	CPUTimeoutSoft time.Duration

	// CPUTimeoutHard is the absolute kill deadline. After it elapses
	// the call ctx is forcibly canceled even if the guest ignored the
	// soft signal. Zero means "no separate hard deadline" — the
	// enforcer falls back to the soft deadline as the only timeout.
	//
	// If both are set, CPUTimeoutHard MUST be >= CPUTimeoutSoft.
	// Validate() rejects the reverse.
	CPUTimeoutHard time.Duration

	// MaxInstancesPerPlugin caps how many live instances of one plugin
	// can coexist. The pool (#9) reads this and rejects checkout once
	// the count is at the cap. Zero means "no instance-count limit".
	MaxInstancesPerPlugin int
}

// Default returns the package's recommended defaults. The returned
// value is a fresh struct; callers can mutate fields without
// disturbing other callers.
func Default() Limits {
	return Limits{
		MemoryPages:           DefaultMemoryPages,
		CPUTimeoutSoft:        DefaultCPUTimeoutSoft,
		CPUTimeoutHard:        DefaultCPUTimeoutHard,
		MaxInstancesPerPlugin: DefaultMaxInstancesPerPlugin,
	}
}

// Validate returns an error if the Limits are internally inconsistent.
// Callers should validate before installing a Limits into the runtime;
// the runtime calls Validate itself as a belt-and-braces check.
func (l Limits) Validate() error {
	if l.CPUTimeoutSoft < 0 {
		return fmt.Errorf("limits: CPUTimeoutSoft must be non-negative, got %v", l.CPUTimeoutSoft)
	}
	if l.CPUTimeoutHard < 0 {
		return fmt.Errorf("limits: CPUTimeoutHard must be non-negative, got %v", l.CPUTimeoutHard)
	}
	if l.CPUTimeoutSoft > 0 && l.CPUTimeoutHard > 0 && l.CPUTimeoutHard < l.CPUTimeoutSoft {
		return fmt.Errorf(
			"limits: CPUTimeoutHard (%v) must be >= CPUTimeoutSoft (%v); a hard deadline that fires before the soft signal gives the guest no chance to unwind",
			l.CPUTimeoutHard, l.CPUTimeoutSoft,
		)
	}
	if l.MaxInstancesPerPlugin < 0 {
		return fmt.Errorf("limits: MaxInstancesPerPlugin must be non-negative, got %d", l.MaxInstancesPerPlugin)
	}
	return nil
}

// EffectiveMemoryPages returns the memory cap that should actually be
// applied. Zero is treated as "use the default" so a Limits{} value
// produced from a partially-populated manifest still yields a working
// runtime.
func (l Limits) EffectiveMemoryPages() uint32 {
	if l.MemoryPages == 0 {
		return DefaultMemoryPages
	}
	return l.MemoryPages
}
