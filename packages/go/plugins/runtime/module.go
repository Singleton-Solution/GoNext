package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/sys"
)

// Module is an instantiated WebAssembly module — one plugin's loaded
// code, ready to receive Call() invocations.
//
// Module is goroutine-safe: concurrent Call from N goroutines on the
// same Module is permitted. Wazero itself documents that
// api.Function.Call is NOT goroutine-safe, so this type guards every
// Call with a sync.Mutex. The serialized contract trades parallelism
// for safety — pool-based callers (#9) keep N Modules around when they
// need true parallel dispatch.
//
// A Module is invalid after Close. Subsequent Call invocations return
// ErrModuleClosed.
type Module struct {
	name     string
	instance api.Module
	compiled compiledHandle
	runtime  *Runtime

	// callMu serializes Call invocations. See type-level comment.
	callMu sync.Mutex

	// closed is set non-zero once Close has run (or the parent Runtime
	// has shut down). Atomic so the fast-path Call check is lock-free.
	closed atomic.Bool
}

// compiledHandle is a tiny interface alias so tests can stub the
// compiled-module side without dragging in a wazero dependency. In
// production this is always wazero.CompiledModule.
type compiledHandle interface {
	Close(context.Context) error
}

// Name returns the unique module name supplied to LoadModule.
func (m *Module) Name() string { return m.name }

// Memory returns a view onto the module's exported linear memory.
//
// Most plugin modules export a memory named "memory" — that's the
// default for every toolchain we expect (Rust, AssemblyScript, TinyGo,
// Go). If the module did not export memory, Memory returns nil; host
// code MUST nil-check.
//
// The returned Memory is owned by the underlying wazero instance.
// Callers must not retain it past the Module's lifetime — after
// Module.Close, dereferencing the memory will panic inside wazero.
func (m *Module) Memory() api.Memory {
	if m.closed.Load() {
		return nil
	}
	return m.instance.Memory()
}

// Call invokes an exported function by name and returns its results
// (as raw uint64s — callers decode via api.DecodeI32/I64/F32/F64).
//
// params are encoded the same way: callers supply uint64 values
// produced by api.EncodeI32/I64/F32/F64. We don't auto-encode because
// the wazero ABI doesn't carry parameter type information at the Call
// boundary; callers are expected to know what they're calling.
//
// Errors:
//
//   - ErrModuleClosed if the module has been closed.
//   - ErrFunctionNotFound if `fnName` is not an exported function.
//   - *TrapError if the guest trapped (panic, OOB memory, division by
//     zero, stack exhaustion, ctx cancellation propagated as a
//     wazero exit).
//   - Other wrapped errors for wazero-internal failures we couldn't
//     classify.
//
// The Module remains usable after a trap — wazero does not poison the
// instance. Callers wanting fail-fast semantics close the module
// themselves on first trap.
func (m *Module) Call(ctx context.Context, fnName string, params ...uint64) ([]uint64, error) {
	if m.closed.Load() {
		return nil, ErrModuleClosed
	}

	fn := m.instance.ExportedFunction(fnName)
	if fn == nil {
		return nil, fmt.Errorf("%w: %s.%s", ErrFunctionNotFound, m.name, fnName)
	}

	m.callMu.Lock()
	defer m.callMu.Unlock()

	// Re-check after acquiring the lock: a concurrent Close could have
	// landed between the atomic load and the mutex grab.
	if m.closed.Load() {
		return nil, ErrModuleClosed
	}

	results, err := fn.Call(ctx, params...)
	if err != nil {
		return nil, m.classifyCallError(fnName, err)
	}
	return results, nil
}

// classifyCallError turns a wazero error into the package's typed
// error vocabulary. The big distinction is "is this a guest-induced
// trap (interesting to the plugin author)" vs. "is this a host-side
// plumbing failure (a runtime bug)". TrapError is the former; bare
// wrapped errors are the latter.
//
// We use the wazero error's textual signature (the "wasm error:" /
// "wasm trap:" prefix or *sys.ExitError type) because wazero does not
// export a single trap-error type to assert against. This is fragile —
// if wazero changes its error wording, we degrade gracefully (every
// Call error is still surfaced, just without the TrapError wrapping).
func (m *Module) classifyCallError(fnName string, err error) error {
	// gn_panic stashes its decoded message into the package-level
	// recorder keyed by module name BEFORE closing the module. If
	// there's a pending recorder, claim it — that's a strictly better
	// reason than wazero's generic "module closed with exit_code(1)".
	if rec := takePanicRecorder(m.name); rec != nil && rec.reason != "" {
		return &TrapError{
			Module: m.name,
			Reason: rec.reason,
			Cause:  err,
		}
	}

	// wazero's sys.ExitError is what surfaces when the module called
	// proc_exit or the runtime forcibly closed the module (e.g., ctx
	// cancellation with WithCloseOnContextDone). We treat it as a trap
	// so callers don't have to type-switch.
	var exitErr *sys.ExitError
	if errors.As(err, &exitErr) {
		return &TrapError{
			Module: m.name,
			Reason: fmt.Sprintf("module exited (code=%d): %v", exitErr.ExitCode(), err),
			Cause:  err,
		}
	}

	// Anything else with the textual signatures wazero uses for
	// guest-side faults — we treat them as a trap. Otherwise it's a
	// generic host-side failure.
	msg := err.Error()
	if containsAny(msg, "wasm error:", "wasm trap:", "unreachable") {
		return &TrapError{
			Module: m.name,
			Reason: fmt.Sprintf("%s.%s: %v", m.name, fnName, err),
			Cause:  err,
		}
	}

	return fmt.Errorf("runtime: call %s.%s: %w", m.name, fnName, err)
}

// containsAny reports whether s contains any of the given substrings.
// Small helper to keep classifyCallError readable.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

// indexOf is a manual substring search to avoid an import of `strings`
// in this file. (Net-zero benefit — but the file is small and the lint
// budget for one extra import is well-spent elsewhere.)
func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	if len(sub) > len(s) {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Close releases the module and removes it from the parent Runtime's
// active set. Idempotent — repeat calls return nil.
//
// Close acquires the call mutex so any in-flight Call completes (or
// fails) before the underlying wazero module is closed. This avoids
// the race where Close + Call interleave and the wazero call sees a
// closed instance partway through.
func (m *Module) Close(ctx context.Context) error {
	if !m.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Drain any in-flight Call. After this Lock returns, no new Call
	// will enter (closed=true is already visible) and the prior call
	// has fully returned.
	m.callMu.Lock()
	defer m.callMu.Unlock()

	if m.runtime != nil {
		m.runtime.removeModule(m.name)
	}

	var firstErr error
	if err := m.instance.Close(ctx); err != nil {
		firstErr = fmt.Errorf("runtime: Close module %q: %w", m.name, err)
	}
	if m.compiled != nil {
		if err := m.compiled.Close(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("runtime: Close compiled %q: %w", m.name, err)
		}
	}
	return firstErr
}

// markClosed is the runtime-shutdown path: the parent Runtime is going
// away and is about to close the wazero runtime, which closes every
// module wholesale. We just need to flip the flag so a stale Module
// handle doesn't keep accepting Call invocations.
//
// We don't acquire callMu here because the wazero runtime's own Close
// will tear down in-flight calls. Acquiring would deadlock when an
// in-flight Call panics through a host function on the same goroutine.
func (m *Module) markClosed() {
	m.closed.Store(true)
}

// IsClosed reports whether the module has been closed (either
// directly or via parent-runtime shutdown).
func (m *Module) IsClosed() bool { return m.closed.Load() }

// WazeroModule returns the underlying api.Module. This is the
// extension point for packages that need direct wazero access —
// principally the capability ABI (#107), which wires host-callback
// state into the module after instantiation.
//
// Returns nil if the module is closed.
func (m *Module) WazeroModule() api.Module {
	if m.closed.Load() {
		return nil
	}
	return m.instance
}
