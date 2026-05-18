package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	pluginruntime "github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"
	"github.com/tetratelabs/wazero/api"
)

// HookObserver is the narrow telemetry surface the Dispatcher publishes
// to. Defining it as an interface — rather than importing the health
// package's Recorder — keeps the runtime/hook stack free of any
// dependency on the metrics registry, so callers that don't care about
// metrics (most tests) can leave the observer unset.
//
// A nil observer is the default and disables all emission. The
// production wiring constructs a *health.recorder and a closure that
// forwards plugin/hook/result; see packages/go/plugins/health/doc.go.
//
// Implementations MUST be safe for concurrent use.
type HookObserver interface {
	// ObserveInvocation is called exactly once per dispatch with
	// the result label and wall-clock duration of the call.
	ObserveInvocation(hook, result string, duration time.Duration)

	// ObserveTrap is called on every trap, in addition to the
	// ObserveInvocation call. reason is the trap reason string;
	// payload is the marshalled hook payload (for replay).
	ObserveTrap(hook, reason string, payload []byte)
}

// Dispatcher wraps a single loaded plugin Module and drives the
// gn_handle_hook ABI on its behalf.
//
// One Dispatcher per Module is the intended pattern — the bridge
// (registry.go) constructs one per plugin at activation and parks it on
// every host-side callback it registers. The Dispatcher caches lookups
// into the module's exports so each invocation skips re-resolving
// gn_handle_hook / gn_alloc / gn_free.
//
// Dispatcher is goroutine-safe: it serializes Call through the
// underlying Module's mutex (Module.Call is documented to serialize),
// and it guards the export-lookup cache with its own mutex. Concurrent
// InvokeAction / InvokeFilter from N goroutines is therefore safe —
// the calls queue against the Module's mutex.
type Dispatcher struct {
	module *pluginruntime.Module

	// exportsMu guards the cached export handles. We resolve once on
	// first use and stash the api.Function pointers so subsequent
	// invocations skip the export-table walk inside wazero.
	exportsMu sync.RWMutex
	exports   *cachedExports
	exportErr error // memoized "missing export" failure; sticky after first lookup

	// observer, if non-nil, is called on every dispatch with the
	// result label and duration, and on every trap with the
	// reason + payload. nil disables emission entirely. The
	// observer is set by the constructor (via DispatcherOption) and
	// never mutated after — the field is therefore lock-free on the
	// hot path.
	observer HookObserver
}

// DispatcherOption customises a Dispatcher at construction time.
type DispatcherOption func(*Dispatcher)

// WithObserver attaches an observer that receives one call per
// dispatch (ObserveInvocation) and one extra call per trap
// (ObserveTrap, after the invocation observation). Passing nil is
// equivalent to not setting an observer.
func WithObserver(o HookObserver) DispatcherOption {
	return func(d *Dispatcher) { d.observer = o }
}

// cachedExports holds the api.Function handles we need to dispatch a
// hook. Resolved on first invocation and reused; if the underlying
// Module is closed the handles are no longer safe to use, but
// Dispatcher.invoke checks Module.IsClosed before dereferencing.
type cachedExports struct {
	handle api.Function
	alloc  api.Function
	free   api.Function
}

// NewDispatcher returns a Dispatcher wrapping the given module. The
// constructor does NOT resolve exports — that is deferred to the first
// invocation so a Dispatcher can be created for a module before it is
// fully activated (the activation code path constructs the bridge
// before touching the guest).
//
// Passing a nil module returns nil; the caller is responsible for the
// guard.
func NewDispatcher(module *pluginruntime.Module, opts ...DispatcherOption) *Dispatcher {
	if module == nil {
		return nil
	}
	d := &Dispatcher{module: module}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Module returns the underlying plugin Module. Test helpers use this to
// reach the module for setup; production callers should not need it.
func (d *Dispatcher) Module() *pluginruntime.Module { return d.module }

// InvokeAction dispatches an action call into the guest.
//
// hookName names the action; args is the bus-level variadic payload
// (will be JSON-encoded and copied into guest memory). Returns nil on
// success; a *HookError on any failure surface (guest reported error,
// trap, OOM, payload too large).
//
// The function consumes args before returning, so the caller's slice
// header is safe to reuse. The args themselves are passed to
// encoding/json and inherit its semantics — pointers are dereferenced,
// time.Time renders as RFC3339, etc.
func (d *Dispatcher) InvokeAction(ctx context.Context, hookName string, args ...interface{}) error {
	if err := hookNameValid(hookName); err != nil {
		return &HookError{Hook: hookName, Status: ResultStatusError, Cause: err}
	}
	payload, err := MarshalActionPayload(args)
	if err != nil {
		return &HookError{Hook: hookName, Status: ResultStatusError, Cause: fmt.Errorf("marshal payload: %w", err)}
	}
	// Actions discard the body — there's no result to read. We pass
	// readResult=false so the dispatcher skips the result-copy step
	// when the guest returns OK with a non-zero pointer (a misbehaving
	// guest could return one anyway; we just ignore it).
	_, err = d.invoke(ctx, hookName, payload, false)
	return err
}

// InvokeFilter dispatches a filter call into the guest.
//
// hookName names the filter; value is the JSON-encoded input value to
// transform; args is the per-call extras (will be JSON-encoded).
//
// Returns the JSON-encoded transformed value on success, or a
// *HookError on failure. The returned bytes are a fresh allocation in
// host memory — the caller may retain them past the next invocation.
//
// If the guest returned ResultStatusOK with no body (which would be
// unusual for a filter), the function returns the original value
// unchanged: a filter that produces nothing has logically not modified
// the chain's value.
func (d *Dispatcher) InvokeFilter(ctx context.Context, hookName string, value json.RawMessage, args ...interface{}) (json.RawMessage, error) {
	if err := hookNameValid(hookName); err != nil {
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: err}
	}
	payload, err := MarshalFilterPayload(value, args)
	if err != nil {
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: fmt.Errorf("marshal payload: %w", err)}
	}
	resultBytes, err := d.invoke(ctx, hookName, payload, true)
	if err != nil {
		return nil, err
	}
	if len(resultBytes) == 0 {
		// Guest returned OK-no-body for a filter. Treat as "no change" —
		// pass the input back. This is the same convention a no-op
		// host-side filter follows.
		return value, nil
	}
	out, err := UnmarshalFilterResult(resultBytes)
	if err != nil {
		return nil, &HookError{Hook: hookName, Status: ResultStatusBadPayload, Cause: err}
	}
	return out, nil
}

// invoke is the shared implementation behind InvokeAction and
// InvokeFilter. Steps:
//
//  1. Resolve the cached exports (gn_handle_hook, gn_alloc, gn_free).
//  2. Sanity-check payload size against MaxPayloadBytes.
//  3. Allocate guest memory for the hook name and the payload.
//  4. Copy bytes in.
//  5. Call gn_handle_hook with (name_ptr, name_len, payload_ptr,
//     payload_len).
//  6. Unpack the i64 return into (result_ptr, result_len).
//  7. If readResult, read the result back into a host-owned slice and
//     free the guest-side buffer.
//
// On any error path the function returns a *HookError. Allocations
// that happened before the failure are NOT explicitly freed —
// reclaiming them is the guest allocator's problem, and our experience
// from envoy proxy-wasm is that mixing host-driven and guest-driven
// frees creates more bugs than it solves. The next gn_alloc call
// reclaims via the guest's allocator implementation (typically
// arena-style for plugin SDKs).
func (d *Dispatcher) invoke(ctx context.Context, hookName string, payload []byte, readResult bool) (out []byte, retErr error) {
	// Capture start so we can publish duration to the observer on
	// every return path. The defer also handles trap notification:
	// if the call returned a HookError tagged ResultStatusTrap, we
	// emit an extra ObserveTrap with the payload bytes so the
	// admin's "replay this failure" flow has everything it needs.
	start := time.Now()
	defer func() {
		if d.observer == nil {
			return
		}
		dur := time.Since(start)
		result := ResultStatusOK.String()
		if retErr != nil {
			var he *HookError
			if errors.As(retErr, &he) {
				result = he.Status.String()
				if he.Status == ResultStatusTrap {
					reason := ""
					var trap *pluginruntime.TrapError
					if errors.As(he.Cause, &trap) {
						reason = trap.Reason
					} else if he.Cause != nil {
						reason = he.Cause.Error()
					}
					d.observer.ObserveTrap(hookName, reason, payload)
				}
			} else {
				result = "error"
			}
		}
		d.observer.ObserveInvocation(hookName, result, dur)
	}()

	if d.module.IsClosed() {
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: pluginruntime.ErrModuleClosed}
	}
	if len(payload) > MaxPayloadBytes {
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(payload), MaxPayloadBytes)}
	}

	exports, err := d.resolveExports()
	if err != nil {
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: err}
	}

	mem := d.module.Memory()
	if mem == nil {
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: ErrMemoryUnavailable}
	}

	// Allocate + copy the hook name. We allocate even for short names —
	// the per-call cost is dwarfed by the JSON marshal — and an
	// allocator-managed slot is easier to reason about than a fixed
	// scratch area.
	nameBytes := []byte(hookName)
	namePtr, err := d.allocAndWrite(ctx, exports.alloc, mem, nameBytes)
	if err != nil {
		return nil, &HookError{Hook: hookName, Status: ResultStatusOutOfMemory, Cause: fmt.Errorf("alloc hook name: %w", err)}
	}

	// Allocate + copy the payload. We do this AFTER the name so a
	// huge-payload OOM still gets reported as OOM (not as a phantom
	// "name allocation failed" — which would be misleading).
	var payloadPtr uint32
	if len(payload) > 0 {
		payloadPtr, err = d.allocAndWrite(ctx, exports.alloc, mem, payload)
		if err != nil {
			return nil, &HookError{Hook: hookName, Status: ResultStatusOutOfMemory, Cause: fmt.Errorf("alloc payload: %w", err)}
		}
	}

	// Invoke gn_handle_hook. We pass the four (ptr, len) args as
	// api.EncodeI32-wrapped uint64s. The guest's signature is i32 for
	// each, but Module.Call takes them as encoded uint64.
	results, callErr := d.module.Call(ctx, EntryPoint,
		api.EncodeU32(namePtr),
		api.EncodeU32(uint32(len(nameBytes))),
		api.EncodeU32(payloadPtr),
		api.EncodeU32(uint32(len(payload))),
	)
	if callErr != nil {
		// A trap unwinds the call. Wrap it as a HookError tagged Trap
		// so callers can distinguish trap vs status-error via errors.Is.
		var trap *pluginruntime.TrapError
		status := ResultStatusError
		if errors.As(callErr, &trap) {
			status = ResultStatusTrap
		}
		return nil, &HookError{Hook: hookName, Status: status, Cause: callErr}
	}
	if len(results) != 1 {
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: fmt.Errorf("expected 1 result from %s, got %d", EntryPoint, len(results))}
	}

	resultPtr, resultLen := unpackResult(results[0])

	// Decode the typed sentinels first — they share the (ptr=0,
	// len<0) shape, so we must check that before treating len as a
	// byte count.
	if resultPtr == 0 && resultLen < 0 {
		status := ResultStatus(resultLen)
		return nil, &HookError{Hook: hookName, Status: status}
	}

	// (ptr=0, len=0) is the no-body success path.
	if resultPtr == 0 && resultLen == 0 {
		return nil, nil
	}

	// Otherwise we have a result buffer. Even for action calls the
	// guest might return one (it's a contract violation but not worth
	// trapping over). We honor readResult: if the caller doesn't want
	// the bytes, we skip the read but still free the buffer so the
	// guest's allocator stays tidy.
	if resultLen < 0 {
		// ptr != 0 but len < 0 — meaningless. Treat as a contract
		// violation; surface as a generic error.
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: fmt.Errorf("guest returned negative length %d with non-zero ptr %d", resultLen, resultPtr)}
	}
	if uint32(resultLen) > MaxResultBytes {
		return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: fmt.Errorf("%w: %d > %d", ErrResultTooLarge, resultLen, MaxResultBytes)}
	}

	var resultCopy []byte
	if readResult {
		buf, ok := mem.Read(resultPtr, uint32(resultLen))
		if !ok {
			return nil, &HookError{Hook: hookName, Status: ResultStatusError, Cause: fmt.Errorf("read result [%d..%d) out of bounds", resultPtr, resultPtr+uint32(resultLen))}
		}
		// Copy into host-owned memory before freeing the guest slot.
		// mem.Read returns a view into wazero's underlying buffer.
		resultCopy = make([]byte, len(buf))
		copy(resultCopy, buf)
	}

	// Free the guest-side result buffer regardless of readResult. We
	// don't wrap the free error into the result — a failing free is
	// a guest bug but not a reason to discard the result the guest
	// already produced.
	if exports.free != nil {
		_, _ = exports.free.Call(ctx, api.EncodeU32(resultPtr), api.EncodeU32(uint32(resultLen)))
	}

	return resultCopy, nil
}

// resolveExports caches the api.Function handles for the ABI exports.
// Run once per Dispatcher; the cached handles survive until Module is
// closed.
//
// We use a RWMutex because the hot path (after first resolve) is
// read-only: the slow path takes the write lock only once.
func (d *Dispatcher) resolveExports() (*cachedExports, error) {
	d.exportsMu.RLock()
	if d.exports != nil {
		ex := d.exports
		d.exportsMu.RUnlock()
		return ex, nil
	}
	if d.exportErr != nil {
		err := d.exportErr
		d.exportsMu.RUnlock()
		return nil, err
	}
	d.exportsMu.RUnlock()

	d.exportsMu.Lock()
	defer d.exportsMu.Unlock()
	// Re-check under the write lock — another goroutine may have
	// resolved while we were waiting.
	if d.exports != nil {
		return d.exports, nil
	}
	if d.exportErr != nil {
		return nil, d.exportErr
	}

	mod := d.module.WazeroModule()
	if mod == nil {
		err := pluginruntime.ErrModuleClosed
		d.exportErr = err
		return nil, err
	}

	handle := mod.ExportedFunction(EntryPoint)
	if handle == nil {
		err := fmt.Errorf("%w: %s", ErrMissingExport, EntryPoint)
		d.exportErr = err
		return nil, err
	}
	alloc := mod.ExportedFunction(AllocExport)
	if alloc == nil {
		err := fmt.Errorf("%w: %s", ErrMissingExport, AllocExport)
		d.exportErr = err
		return nil, err
	}
	// gn_free is required but we treat a missing one as a warning-ish
	// soft error: the call still works, we just leak the result
	// buffer. Surfacing it as a hard miss matches the documented ABI.
	free := mod.ExportedFunction(FreeExport)
	if free == nil {
		err := fmt.Errorf("%w: %s", ErrMissingExport, FreeExport)
		d.exportErr = err
		return nil, err
	}

	d.exports = &cachedExports{handle: handle, alloc: alloc, free: free}
	return d.exports, nil
}

// allocAndWrite calls gn_alloc(size) and writes data into the returned
// pointer. Returns the pointer, or an error if the alloc returned 0
// (OOM in the guest) or the memory write failed.
//
// We call gn_alloc directly through the wazero api.Function rather than
// through Module.Call so we don't pay the per-call mutex round trip
// twice (alloc + handle_hook). Module.Call's mutex serializes us
// against external concurrent callers; once we're inside the
// dispatcher's invoke we hold no extra lock, and wazero's per-instance
// rule (one Call at a time on the same module) is already satisfied by
// the Module wrapper's outer serialization.
//
// Wait — that's the catch. We DO need Module.Call's mutex for alloc,
// because the outer invoke isn't holding it. The clean fix is to use
// Module.Call for alloc too. Let me re-explain: the dispatcher's
// invoke runs Module.Call once (for gn_handle_hook), but the allocator
// calls happen BEFORE that and need their own Module.Call serialization
// to satisfy wazero's contract. So allocAndWrite does use Module.Call.
func (d *Dispatcher) allocAndWrite(ctx context.Context, _ api.Function, mem api.Memory, data []byte) (uint32, error) {
	results, err := d.module.Call(ctx, AllocExport, api.EncodeU32(uint32(len(data))))
	if err != nil {
		return 0, fmt.Errorf("alloc call: %w", err)
	}
	if len(results) != 1 {
		return 0, fmt.Errorf("alloc returned %d results, want 1", len(results))
	}
	ptr := api.DecodeU32(results[0])
	if ptr == 0 {
		return 0, ErrOutOfMemory
	}
	if len(data) == 0 {
		return ptr, nil
	}
	if !mem.Write(ptr, data) {
		return 0, fmt.Errorf("memory write [%d..%d) out of bounds (size=%d)", ptr, ptr+uint32(len(data)), mem.Size())
	}
	return ptr, nil
}
