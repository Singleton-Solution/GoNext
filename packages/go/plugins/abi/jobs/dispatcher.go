package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	pluginruntime "github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime"
	"github.com/tetratelabs/wazero/api"
)

// JobObserver is the narrow telemetry surface the Dispatcher publishes
// to. Defining it as an interface — rather than importing the health
// package's Recorder — keeps the runtime/job stack free of any
// dependency on the metrics registry, so callers that don't care about
// metrics (most tests) can leave the observer unset.
//
// A nil observer is the default and disables all emission. The
// production wiring constructs a recorder that forwards
// plugin/job/result; the same pattern as the hooks Dispatcher.
//
// Implementations MUST be safe for concurrent use.
type JobObserver interface {
	// ObserveInvocation is called exactly once per dispatch with
	// the result label and wall-clock duration of the call.
	ObserveInvocation(job, result string, duration time.Duration)

	// ObserveTrap is called on every trap, in addition to the
	// ObserveInvocation call. reason is the trap reason string;
	// payload is the marshalled envelope bytes (for replay).
	ObserveTrap(job, reason string, payload []byte)
}

// Dispatcher wraps a single loaded plugin Module and drives the
// gn_handle_job ABI on its behalf.
//
// One Dispatcher per Module is the intended pattern — the bridge
// (bridge.go) constructs one per plugin at activation and parks it on
// every TaskSpec it registers. The Dispatcher caches lookups into the
// module's exports so each invocation skips re-resolving gn_handle_job
// / gn_alloc / gn_free.
//
// Dispatcher is goroutine-safe: it serializes Call through the
// underlying Module's mutex (Module.Call is documented to serialize),
// and it guards the export-lookup cache with its own mutex. Concurrent
// InvokeJob from N goroutines is therefore safe — the calls queue
// against the Module's mutex. This is the key correctness property for
// the race test: even when 100 asynq workers pick up tasks for the
// same plugin simultaneously, the WASM module sees a serialized stream
// of invocations.
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
	observer JobObserver
}

// DispatcherOption customises a Dispatcher at construction time.
type DispatcherOption func(*Dispatcher)

// WithObserver attaches an observer that receives one call per
// dispatch (ObserveInvocation) and one extra call per trap
// (ObserveTrap, after the invocation observation). Passing nil is
// equivalent to not setting an observer.
func WithObserver(o JobObserver) DispatcherOption {
	return func(d *Dispatcher) { d.observer = o }
}

// cachedExports holds the api.Function handles we need to dispatch a
// job. Resolved on first invocation and reused; if the underlying
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

// InvokeJob dispatches a job call into the guest.
//
// jobName names the task type registered in the TaskSpec; envelope is
// the pre-marshalled JobEnvelope bytes (see MarshalJobEnvelope). The
// envelope includes the idempotency key, retry count, and the raw
// producer payload — the bridge always builds it before reaching here,
// so direct callers (tests, future producers) must marshal too.
//
// Returns nil on success; a *JobError on any failure surface (guest
// reported error, trap, OOM, payload too large).
//
// The returned bytes (rare for jobs — see ABI doc) are discarded. If a
// future caller wants the optional result body, lift this to return
// ([]byte, error) without a wire break.
func (d *Dispatcher) InvokeJob(ctx context.Context, jobName string, envelope []byte) error {
	if err := jobNameValid(jobName); err != nil {
		return &JobError{Job: jobName, Status: ResultStatusError, Cause: err}
	}
	_, err := d.invoke(ctx, jobName, envelope, false)
	return err
}

// invoke is the shared implementation behind InvokeJob and any future
// job entry points (an InvokeJobWithResult, say). Steps:
//
//  1. Resolve the cached exports (gn_handle_job, gn_alloc, gn_free).
//  2. Sanity-check payload size against MaxPayloadBytes.
//  3. Allocate guest memory for the job name and the payload.
//  4. Copy bytes in.
//  5. Call gn_handle_job with (name_ptr, name_len, payload_ptr,
//     payload_len).
//  6. Unpack the i64 return into (result_ptr, result_len).
//  7. If readResult, read the result back into a host-owned slice and
//     free the guest-side buffer.
//
// On any error path the function returns a *JobError. Allocations
// that happened before the failure are NOT explicitly freed —
// reclaiming them is the guest allocator's problem. Matches the hooks
// bridge convention.
func (d *Dispatcher) invoke(ctx context.Context, jobName string, payload []byte, readResult bool) (out []byte, retErr error) {
	// Capture start so we can publish duration to the observer on
	// every return path. The defer also handles trap notification:
	// if the call returned a JobError tagged ResultStatusTrap, we
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
			var je *JobError
			if errors.As(retErr, &je) {
				result = je.Status.String()
				if je.Status == ResultStatusTrap {
					reason := ""
					var trap *pluginruntime.TrapError
					if errors.As(je.Cause, &trap) {
						reason = trap.Reason
					} else if je.Cause != nil {
						reason = je.Cause.Error()
					}
					d.observer.ObserveTrap(jobName, reason, payload)
				}
			} else {
				result = "error"
			}
		}
		d.observer.ObserveInvocation(jobName, result, dur)
	}()

	if d.module.IsClosed() {
		return nil, &JobError{Job: jobName, Status: ResultStatusError, Cause: pluginruntime.ErrModuleClosed}
	}
	if len(payload) > MaxPayloadBytes {
		return nil, &JobError{Job: jobName, Status: ResultStatusError, Cause: fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(payload), MaxPayloadBytes)}
	}

	exports, err := d.resolveExports()
	if err != nil {
		return nil, &JobError{Job: jobName, Status: ResultStatusError, Cause: err}
	}

	mem := d.module.Memory()
	if mem == nil {
		return nil, &JobError{Job: jobName, Status: ResultStatusError, Cause: ErrMemoryUnavailable}
	}

	// Allocate + copy the job name. We allocate even for short names —
	// the per-call cost is dwarfed by the JSON marshal — and an
	// allocator-managed slot is easier to reason about than a fixed
	// scratch area.
	nameBytes := []byte(jobName)
	namePtr, err := d.allocAndWrite(ctx, mem, nameBytes)
	if err != nil {
		return nil, &JobError{Job: jobName, Status: ResultStatusOutOfMemory, Cause: fmt.Errorf("alloc job name: %w", err)}
	}

	// Allocate + copy the payload.
	var payloadPtr uint32
	if len(payload) > 0 {
		payloadPtr, err = d.allocAndWrite(ctx, mem, payload)
		if err != nil {
			return nil, &JobError{Job: jobName, Status: ResultStatusOutOfMemory, Cause: fmt.Errorf("alloc payload: %w", err)}
		}
	}

	// Invoke gn_handle_job. We pass the four (ptr, len) args as
	// api.EncodeI32-wrapped uint64s. The guest's signature is i32 for
	// each, but Module.Call takes them as encoded uint64.
	results, callErr := d.module.Call(ctx, EntryPoint,
		api.EncodeU32(namePtr),
		api.EncodeU32(uint32(len(nameBytes))),
		api.EncodeU32(payloadPtr),
		api.EncodeU32(uint32(len(payload))),
	)
	if callErr != nil {
		// A trap unwinds the call. Wrap it as a JobError tagged Trap
		// so callers can distinguish trap vs status-error via errors.Is.
		var trap *pluginruntime.TrapError
		status := ResultStatusError
		if errors.As(callErr, &trap) {
			status = ResultStatusTrap
		}
		return nil, &JobError{Job: jobName, Status: status, Cause: callErr}
	}
	if len(results) != 1 {
		return nil, &JobError{Job: jobName, Status: ResultStatusError, Cause: fmt.Errorf("expected 1 result from %s, got %d", EntryPoint, len(results))}
	}

	resultPtr, resultLen := unpackResult(results[0])

	// Decode the typed sentinels first — they share the (ptr=0,
	// len<0) shape, so we must check that before treating len as a
	// byte count.
	if resultPtr == 0 && resultLen < 0 {
		status := ResultStatus(resultLen)
		return nil, &JobError{Job: jobName, Status: status}
	}

	// (ptr=0, len=0) is the no-body success path.
	if resultPtr == 0 && resultLen == 0 {
		return nil, nil
	}

	// Otherwise we have a result buffer. Even for jobs that aren't
	// supposed to produce one, a misbehaving guest might. We honor
	// readResult: if the caller doesn't want the bytes, we skip the
	// read but still free the buffer so the guest's allocator stays
	// tidy.
	if resultLen < 0 {
		// ptr != 0 but len < 0 — meaningless.
		return nil, &JobError{Job: jobName, Status: ResultStatusError, Cause: fmt.Errorf("guest returned negative length %d with non-zero ptr %d", resultLen, resultPtr)}
	}
	if uint32(resultLen) > MaxResultBytes {
		return nil, &JobError{Job: jobName, Status: ResultStatusError, Cause: fmt.Errorf("%w: %d > %d", ErrResultTooLarge, resultLen, MaxResultBytes)}
	}

	var resultCopy []byte
	if readResult {
		buf, ok := mem.Read(resultPtr, uint32(resultLen))
		if !ok {
			return nil, &JobError{Job: jobName, Status: ResultStatusError, Cause: fmt.Errorf("read result [%d..%d) out of bounds", resultPtr, resultPtr+uint32(resultLen))}
		}
		// Copy into host-owned memory before freeing the guest slot.
		resultCopy = make([]byte, len(buf))
		copy(resultCopy, buf)
	}

	// Free the guest-side result buffer regardless of readResult.
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
// We route alloc through Module.Call rather than the cached
// api.Function so wazero's per-instance serialization remains uniform —
// the same approach as the hooks dispatcher.
func (d *Dispatcher) allocAndWrite(ctx context.Context, mem api.Memory, data []byte) (uint32, error) {
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
