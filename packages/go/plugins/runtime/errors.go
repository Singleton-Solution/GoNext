package runtime

import (
	"errors"
	"fmt"
)

// ErrModuleClosed is returned by Module.Call when the underlying wazero
// module has already been closed. Callers can match with errors.Is.
var ErrModuleClosed = errors.New("runtime: module is closed")

// ErrRuntimeClosed is returned by Runtime.LoadModule when the Runtime
// itself has been closed.
var ErrRuntimeClosed = errors.New("runtime: runtime is closed")

// ErrFunctionNotFound is returned by Module.Call when the requested
// export name does not exist on the module, or exists but is not a
// function.
var ErrFunctionNotFound = errors.New("runtime: exported function not found")

// ErrMemoryNotExported is returned by host functions (and by
// Module.Memory) when the guest module did not export a linear memory.
// Guest authors that want to use any host function which reads/writes
// strings (gn_log, gn_panic) MUST export `memory`.
var ErrMemoryNotExported = errors.New("runtime: module did not export linear memory")

// TrapError wraps a WebAssembly trap surfaced through wazero — a guest
// panic via gn_panic, division by zero, out-of-bounds memory access,
// stack exhaustion, etc.
//
// Reason is the human-readable description of the trap. For
// gn_panic-originated traps it is the message the guest passed in. For
// wazero-originated traps it is the wazero error string.
//
// Module is the name of the module that trapped, populated so a caller
// receiving a TrapError without context can attribute it.
//
// The underlying wazero error (when present) is unwrappable via
// errors.Unwrap for callers that need to inspect *sys.ExitError or
// other wazero-internal types.
type TrapError struct {
	Module string
	Reason string
	Cause  error
}

// Error returns a one-line description of the trap.
func (e *TrapError) Error() string {
	if e.Module == "" {
		return fmt.Sprintf("runtime: wasm trap: %s", e.Reason)
	}
	return fmt.Sprintf("runtime: wasm trap in %q: %s", e.Module, e.Reason)
}

// Unwrap returns the underlying wazero error, if any.
func (e *TrapError) Unwrap() error { return e.Cause }

// HostError is returned by a host function that could not satisfy a
// guest call (e.g., the guest asked gn_log to read from an out-of-bounds
// memory address). The host function records the HostError and traps
// the guest so the failure propagates as a TrapError to Module.Call.
//
// HostError is exported so test code that drives host functions
// directly can match the value.
type HostError struct {
	Function string
	Reason   string
}

// Error returns a one-line description of the host error.
func (e *HostError) Error() string {
	return fmt.Sprintf("runtime: host function %s: %s", e.Function, e.Reason)
}

// CompileError wraps a wazero CompileModule failure. The bytes were
// rejected as not being a valid WebAssembly binary. The Cause is the
// wazero error string for diagnostic logs.
type CompileError struct {
	Module string
	Cause  error
}

// Error returns a one-line description of the compile failure.
func (e *CompileError) Error() string {
	if e.Module == "" {
		return fmt.Sprintf("runtime: compile failed: %v", e.Cause)
	}
	return fmt.Sprintf("runtime: compile %q failed: %v", e.Module, e.Cause)
}

// Unwrap returns the underlying wazero error.
func (e *CompileError) Unwrap() error { return e.Cause }
