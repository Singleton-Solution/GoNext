package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tetratelabs/wazero/api"
)

// Log severity levels used by gn_log. Mirroring slog's levels keeps
// the host-side mapping trivial.
const (
	logLevelDebug int32 = 0
	logLevelInfo  int32 = 1
	logLevelWarn  int32 = 2
	logLevelError int32 = 3
)

// maxHostStringLen is the cap on bytes a host function will read from
// guest linear memory in a single (ptr, len) pair. 64 KiB is generous
// for log messages and panic reasons; anything larger is almost
// certainly a malformed pointer.
const maxHostStringLen = 64 * 1024

// panicRecorder is the per-Call container for whatever the guest passed
// to gn_panic before unwinding. We can't return data out of a trapping
// host function via normal returns, so we stash it on the ctx-attached
// recorder and pluck it out on the way up.
type panicRecorder struct {
	mu     sync.Mutex
	reason string
}

// record stores the panic reason. Safe to call from any goroutine
// because Module.Call serializes invocations on a per-module mutex —
// but we still use a fine-grained lock here so a future change to
// drop the per-module serialization (e.g. when wazero gains a thread
// model) doesn't introduce a quiet race.
func (p *panicRecorder) record(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reason = reason
}

// registerEnvHost wires the built-in "env" host module onto rt's
// wazero runtime.
//
// Functions registered:
//
//	gn_log(level i32, ptr i32, len i32)     — write a host-side log line
//	gn_panic(ptr i32, len i32)              — trap with a guest message
//	gn_time_ms() -> i64                     — monotonic-ish wall-clock ms
//
// gn_log and gn_panic both read their string argument from the calling
// module's exported linear memory at [ptr, ptr+len). If the read is
// out of bounds or no memory is exported, the host function records a
// HostError and traps the guest. That way a buggy guest gets a clear
// trap rather than silent UB.
func (r *Runtime) registerEnvHost(ctx context.Context) error {
	b := r.wazeroRT.NewHostModuleBuilder(hostModuleName)

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(r.hostGnLog),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
			nil).
		WithParameterNames("level", "ptr", "len").
		Export("gn_log")

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(r.hostGnPanic),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
			nil).
		WithParameterNames("ptr", "len").
		Export("gn_panic")

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(r.hostGnTimeMs),
			nil,
			[]api.ValueType{api.ValueTypeI64}).
		Export("gn_time_ms")

	// Layer the observability ABI (gn_i18n_translate, gn_metric_observe,
	// gn_event_emit, gn_span_event) onto the same env builder so the
	// guest imports them under one namespace. The function bodies live
	// in host_observability.go and read per-Runtime configuration from
	// the package-level observabilityRegistry side map, so this call
	// does not require any constructor-time wiring on Runtime itself.
	r.registerObservabilityHost(b)

	// Platform ABIs (gn_secrets_get, plus gn_audit_emit and
	// gn_cron_register in follow-up commits) live in the same "env"
	// namespace so guests find every gn_* import under the same
	// module. Exports are only added when the runtime was constructed
	// WithPlatform AND the matching service is configured; otherwise
	// they're absent and a guest importing them fails at instantiation
	// with the standard wazero "missing import" error, which is the
	// correct outcome for "platform not configured".
	r.addPlatformExports(b)

	if _, err := b.Instantiate(ctx); err != nil {
		return fmt.Errorf("instantiate %q host module: %w", hostModuleName, err)
	}
	return nil
}

// readHostString reads [ptr, ptr+length) out of the calling module's
// memory and returns the byte slice. The slice points into wazero's
// underlying buffer — callers that need to retain the data past the
// host call must copy it.
//
// Returns nil, *HostError on out-of-bounds, missing memory, or
// pathologically-long requests.
func readHostString(fnName string, mod api.Module, ptr, length uint32) ([]byte, error) {
	if length == 0 {
		return nil, nil
	}
	if length > maxHostStringLen {
		return nil, &HostError{
			Function: fnName,
			Reason:   fmt.Sprintf("string length %d exceeds host cap %d", length, maxHostStringLen),
		}
	}
	mem := mod.Memory()
	if mem == nil {
		return nil, &HostError{Function: fnName, Reason: ErrMemoryNotExported.Error()}
	}
	buf, ok := mem.Read(ptr, length)
	if !ok {
		return nil, &HostError{
			Function: fnName,
			Reason:   fmt.Sprintf("memory read [%d..%d) out of bounds (size=%d)", ptr, ptr+length, mem.Size()),
		}
	}
	return buf, nil
}

// hostGnLog implements env.gn_log. Signature: (level i32, ptr i32, len i32).
//
// The level argument maps onto slog's levels (debug/info/warn/error);
// unknown levels are routed to info. Out-of-bounds string reads emit a
// host-side warning but DO NOT trap — log calls are intentionally
// best-effort so a misbehaving plugin can't bring the host down by
// logging junk.
func (r *Runtime) hostGnLog(ctx context.Context, mod api.Module, stack []uint64) {
	level := api.DecodeI32(stack[0])
	ptr := api.DecodeU32(stack[1])
	length := api.DecodeU32(stack[2])

	buf, err := readHostString("gn_log", mod, ptr, length)
	if err != nil {
		r.logger.Warn("runtime: gn_log: bad string args",
			slog.String("module", mod.Name()),
			slog.String("err", err.Error()))
		return
	}

	msg := string(buf)
	switch level {
	case logLevelDebug:
		r.logger.Debug(msg, slog.String("plugin", mod.Name()))
	case logLevelWarn:
		r.logger.Warn(msg, slog.String("plugin", mod.Name()))
	case logLevelError:
		r.logger.Error(msg, slog.String("plugin", mod.Name()))
	case logLevelInfo:
		fallthrough
	default:
		r.logger.Info(msg, slog.String("plugin", mod.Name()))
	}

	// Fan the event out to the optional LogPublisher (the dev CLI's
	// log-streaming endpoint subscribes here). Publish MUST be
	// non-blocking; the contract is documented on the LogPublisher
	// interface. We deliberately do this AFTER the slog write so a
	// misbehaving subscriber can't suppress the structured log.
	if r.logPublisher != nil {
		r.logPublisher.Publish(mod.Name(), level, msg)
	}
}

// hostGnPanic implements env.gn_panic. Signature: (ptr i32, len i32).
//
// The host reads the panic reason from guest memory, attaches it to a
// recorder, and CLOSES the calling module with a non-zero exit code.
// That close turns into a *sys.ExitError on the caller's Call(), which
// classifyCallError can promote into a *TrapError carrying the
// recorded reason.
//
// Why close instead of letting wazero unwind a trap naturally? wazero
// host functions don't have a "trap immediately" primitive that
// preserves a custom error payload; CloseWithExitCode is the documented
// way to terminate the guest from inside a host call.
func (r *Runtime) hostGnPanic(ctx context.Context, mod api.Module, stack []uint64) {
	ptr := api.DecodeU32(stack[0])
	length := api.DecodeU32(stack[1])

	rec := &panicRecorder{}
	if buf, err := readHostString("gn_panic", mod, ptr, length); err == nil {
		rec.record(string(buf))
	} else {
		// Even on a bad pointer we still want to trap — a guest that
		// called gn_panic intends to die. Record the host error as
		// the reason so the operator sees what happened.
		rec.record(err.Error())
	}

	// Stash the recorder on the ctx so classifyCallError can find it
	// alongside the error path. The error we return from a host
	// function via stack-mutation isn't what surfaces to the caller —
	// we have to close the module to trigger the trap, and the
	// ExitError that pops out doesn't carry our payload. So we hang
	// the recorder on a side channel: a registered moduleRegistry
	// keyed by module name. We don't actually use ctx here — the
	// recorder is held by hostPanicError below, which the runtime
	// surfaces via Module.classifyCallError.
	r.registerPanicRecorder(mod.Name(), rec)

	// Close the module to abort the in-flight call. Exit code 1
	// signals "non-clean exit" to wazero, which turns into a
	// *sys.ExitError on the caller's Call return.
	_ = mod.CloseWithExitCode(ctx, 1)
}

// hostGnTimeMs implements env.gn_time_ms. Signature: () -> i64.
//
// Returns Unix milliseconds. The wazero ABI requires us to write the
// result back to stack[0] as a uint64; api.EncodeI64 handles signedness.
//
// Plugins that need a monotonic clock for measuring durations should
// use this — but be aware that the underlying source is time.Now (or
// whatever WithTimeSource injected), which is wall-clock and can
// jump backwards on NTP corrections. A true monotonic source belongs
// in the capability ABI (#107).
func (r *Runtime) hostGnTimeMs(_ context.Context, _ api.Module, stack []uint64) {
	ms := r.timeSource().UnixMilli()
	stack[0] = api.EncodeI64(ms)
}

// registerPanicRecorder attaches the recorder to the runtime's per-name
// map. Module.Call drains it on the way up if the call returned an
// error. Concurrent access on the same module is impossible because
// Module.Call serializes — but two different modules can panic
// simultaneously, hence the mutex.
//
// We keep this off the Runtime struct (it's its own concern) and use a
// package-level sync.Map keyed by module name. Each entry is consumed
// exactly once by the next Call's error path.
var panicRecorders sync.Map // map[string]*panicRecorder

func (r *Runtime) registerPanicRecorder(modName string, rec *panicRecorder) {
	panicRecorders.Store(modName, rec)
}

// takePanicRecorder pulls and clears the recorder for a module.
// Called from Module.classifyCallError on the error path.
func takePanicRecorder(modName string) *panicRecorder {
	if v, ok := panicRecorders.LoadAndDelete(modName); ok {
		return v.(*panicRecorder)
	}
	return nil
}

// Compile-time check that the host function shape matches what wazero
// expects. If wazero ever changes its GoModuleFunc signature, this
// fails at build time rather than at registration.
var _ api.GoModuleFunction = api.GoModuleFunc(func(context.Context, api.Module, []uint64) {})
