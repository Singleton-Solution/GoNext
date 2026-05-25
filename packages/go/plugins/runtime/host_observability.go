// host_observability.go owns the WASM-facing observability ABI:
// gn_i18n_translate (this commit), gn_metric_observe / gn_event_emit /
// gn_span_event (follow-up commits), plus the host-side trap-event
// capture path.
//
// Kept in its own file because parallel ABI work (capability, data,
// platform) lands in sibling host_*.go files; splitting by domain
// avoids three-way merge friction on the runtime package's main host.go.
//
// The exports live in the same "env" host module as the baseline
// gn_log/gn_panic/gn_time_ms. From a plugin author's view they are
// simply more host functions on the same import namespace.
//
// State model: rather than mutate the Runtime struct (touched by every
// parallel ABI effort), observability state hangs off a package-level
// sync.Map keyed by *Runtime. The hot path is one per-call Load.
// UseObservability() installs the state on a constructed Runtime via
// the same map — there is no constructor-time Runtime field for
// observability, so adding more sinks in a follow-up touches only
// this file and its sibling test file.

package runtime

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Singleton-Solution/GoNext/packages/go/i18n"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// AuditEmitter is the narrow audit-log surface the runtime fans
// observability events into (traps, and — in follow-up commits —
// metric drops and gn_event_emit calls). We define it as an
// interface so the runtime stays decoupled from the concrete
// audit.Store implementation.
//
// Implementations MUST be safe for concurrent use.
type AuditEmitter interface {
	// EmitPluginEvent writes one audit row attributed to slug. severity
	// is one of "info", "warning", "critical" — matching the audit
	// package's Severity values.
	EmitPluginEvent(ctx context.Context, slug, eventType, severity string, metadata map[string]any) error
}

// observability is the per-Runtime sink bundle stored in the
// observabilityRegistry. Every field is nil-safe: the host functions
// branch on "no sink wired" before touching the field.
//
// More sinks land in follow-up commits — a SpanEventReceiver for
// gn_span_event (#194), a *PluginMetrics for #181's Prometheus
// counters, and a *CardinalityDam for #226's gn_metric_observe.
type observability struct {
	translator   i18n.Translator
	auditEmitter AuditEmitter
}

// observabilityRegistry is the side-channel state for observability
// configuration. We store per-Runtime sinks here rather than on the
// Runtime struct so parallel ABI agents can land their own host_*.go
// files without rebasing through the central runtime.go.
//
// The slot is populated lazily on the first observability-related
// access; obsFor returns a zero-value observability when none has
// been configured, keeping the host functions safe to call against
// any Runtime.
var observabilityRegistry sync.Map // map[*Runtime]*observability

// obsFor returns the per-Runtime observability bundle, creating a
// default one on first access so the host functions can read fields
// unconditionally. The default is: NoopTranslator (key passthrough),
// no audit emitter.
func obsFor(r *Runtime) *observability {
	if v, ok := observabilityRegistry.Load(r); ok {
		return v.(*observability)
	}
	o := &observability{
		translator: i18n.NoopTranslator{},
	}
	actual, _ := observabilityRegistry.LoadOrStore(r, o)
	return actual.(*observability)
}

// mutateObs runs fn against the per-Runtime observability bundle,
// installing a fresh one if none exists. Used by the With* builder
// methods below to fold configuration in atomically.
func mutateObs(r *Runtime, fn func(*observability)) {
	o := obsFor(r)
	fn(o)
}

// ===========================================================================
// Builder. The observability state hangs off the Runtime pointer, which
// doesn't exist when option callbacks would fire — so we expose a
// fluent post-construction builder instead of runtime.With* options.
// ===========================================================================

// UseObservability is a fluent builder that installs observability
// sinks on a constructed Runtime. Returns the builder for chaining.
//
// Calling pattern:
//
//	rt, _ := runtime.New(ctx, ...)
//	rt.UseObservability().
//	    WithTranslator(myTranslator).
//	    WithAuditEmitter(emitter)
func (r *Runtime) UseObservability() *ObservabilityBuilder {
	return &ObservabilityBuilder{rt: r}
}

// ObservabilityBuilder is the fluent configuration handle returned
// by Runtime.UseObservability. Each method installs one seam and
// returns the builder for chaining.
type ObservabilityBuilder struct {
	rt *Runtime
}

// WithTranslator installs an i18n.Translator. Passing nil reverts to
// the NoopTranslator default.
func (b *ObservabilityBuilder) WithTranslator(t i18n.Translator) *ObservabilityBuilder {
	mutateObs(b.rt, func(o *observability) {
		if t == nil {
			t = i18n.NoopTranslator{}
		}
		o.translator = t
	})
	return b
}

// WithAuditEmitter installs the audit sink for trap (and, in
// follow-up commits, metric-drop / event-emit) observations. Passing
// nil disables audit emission.
func (b *ObservabilityBuilder) WithAuditEmitter(e AuditEmitter) *ObservabilityBuilder {
	mutateObs(b.rt, func(o *observability) { o.auditEmitter = e })
	return b
}

// ===========================================================================
// Accessors.
// ===========================================================================

// Translator returns the configured i18n.Translator. Never nil — a
// runtime that never called UseObservability sees the NoopTranslator
// default.
func (r *Runtime) Translator() i18n.Translator { return obsFor(r).translator }

// AuditEmitter returns the configured audit emitter or nil.
func (r *Runtime) AuditEmitter() AuditEmitter { return obsFor(r).auditEmitter }

// ===========================================================================
// Registration helper.
// ===========================================================================

// registerObservabilityHost wires the observability ABI exports onto
// the supplied wazero.HostModuleBuilder. Called from registerEnvHost
// so the exports land in the same env module as the baseline trio.
//
// Exports added this commit:
//
//	gn_i18n_translate(key_ptr i32, key_len i32, locale_ptr i32, locale_len i32) -> i64
//
// The remaining observability exports (gn_metric_observe, gn_event_emit,
// gn_span_event) ship in follow-up commits — they share the same
// "env" namespace and the same registration helper.
//
// gn_i18n_translate returns a packed (ptr, len) i64 mirroring the
// gn_handle_hook ABI: the host writes the translated string into
// guest memory via gn_alloc and packs the (ptr, len) into the i64
// return. The high 32 bits are the pointer, the low 32 bits are the
// length. (0, 0) signals "no translation found, return key as-is" so
// the guest SDK can do the fallback locally without a second host hop.
func (r *Runtime) registerObservabilityHost(b wazero.HostModuleBuilder) {
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(r.hostGnI18nTranslate),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
			[]api.ValueType{api.ValueTypeI64}).
		WithParameterNames("key_ptr", "key_len", "locale_ptr", "locale_len").
		Export("gn_i18n_translate")
}

// ===========================================================================
// Host-function bodies.
// ===========================================================================

// hostGnI18nTranslate implements env.gn_i18n_translate.
// Signature: (key_ptr i32, key_len i32, locale_ptr i32, locale_len i32) -> i64.
//
// The host reads (key, locale) from guest memory, asks the configured
// Translator for the localised string, and — if a translation exists
// — writes it back into guest memory via gn_alloc. The packed return
// is (alloc_ptr << 32) | len.
//
// Falls back to (0, 0) when:
//   - Either string is empty or out of bounds.
//   - The Translator returned the key unchanged (i.e. no entry).
//   - gn_alloc returned 0 (OOM); the guest SDK should treat the
//     "no translation" path identically.
//
// We never trap on i18n-related failure — translation is intentionally
// best-effort so a missing catalogue can't take down a plugin.
func (r *Runtime) hostGnI18nTranslate(ctx context.Context, mod api.Module, stack []uint64) {
	keyPtr := api.DecodeU32(stack[0])
	keyLen := api.DecodeU32(stack[1])
	localePtr := api.DecodeU32(stack[2])
	localeLen := api.DecodeU32(stack[3])

	o := obsFor(r)
	slug := mod.Name()

	keyBuf, err := readHostString("gn_i18n_translate", mod, keyPtr, keyLen)
	if err != nil {
		r.logger.Warn("gn_i18n_translate: bad key args",
			slog.String("plugin", slug),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI64(0)
		return
	}
	localeBuf, err := readHostString("gn_i18n_translate", mod, localePtr, localeLen)
	if err != nil {
		r.logger.Warn("gn_i18n_translate: bad locale args",
			slog.String("plugin", slug),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI64(0)
		return
	}

	key := string(keyBuf)
	locale := string(localeBuf)
	translated := o.translator.Translate(key, locale)

	// No translation available: signal (0, 0) so the guest SDK can
	// surface its own fallback (typically the key, formatted client-
	// side). Writing it back from the host would be wasteful for a
	// pure-passthrough case.
	if translated == "" || translated == key {
		stack[0] = api.EncodeI64(0)
		return
	}

	// Allocate guest memory via gn_alloc, copy the bytes in. If the
	// guest didn't export gn_alloc — common for stripped-down test
	// modules — fall back to (0, 0). That's the same behaviour as
	// "no translation found" and keeps the contract uniform.
	ptr, ok := allocAndWriteGuest(ctx, mod, []byte(translated))
	if !ok {
		stack[0] = api.EncodeI64(0)
		return
	}
	stack[0] = api.EncodeI64(int64(uint64(ptr)<<32 | uint64(uint32(len(translated)))))
}

// ===========================================================================
// Helpers.
// ===========================================================================

// allocAndWriteGuest is the host-side analogue of the dispatcher's
// allocAndWrite: it asks the guest's gn_alloc for `len(data)` bytes
// and writes data into the returned pointer. Returns (ptr, true) on
// success; (0, false) on any failure.
//
// Unlike the dispatcher we don't go through the per-module mutex —
// we're already inside a host function called from within a
// Module.Call, so the mutex is held by the outer call. Re-acquiring
// would deadlock.
func allocAndWriteGuest(ctx context.Context, mod api.Module, data []byte) (uint32, bool) {
	if len(data) == 0 {
		return 0, false
	}
	alloc := mod.ExportedFunction("gn_alloc")
	if alloc == nil {
		return 0, false
	}
	results, err := alloc.Call(ctx, api.EncodeU32(uint32(len(data))))
	if err != nil || len(results) != 1 {
		return 0, false
	}
	ptr := api.DecodeU32(results[0])
	if ptr == 0 {
		return 0, false
	}
	mem := mod.Memory()
	if mem == nil {
		return 0, false
	}
	if !mem.Write(ptr, data) {
		return 0, false
	}
	return ptr, true
}

// emitObservabilityAudit forwards an event to the configured
// AuditEmitter, with best-effort error logging. A nil emitter is the
// "no audit configured" state and silently no-ops (returns nil) —
// the plugin's log line and metric counter are still bumped, so the
// event is not lost wholesale.
func emitObservabilityAudit(ctx context.Context, r *Runtime, slug, eventType, severity string, metadata map[string]any) error {
	o := obsFor(r)
	if o.auditEmitter == nil {
		return nil
	}
	if err := o.auditEmitter.EmitPluginEvent(ctx, slug, eventType, severity, metadata); err != nil {
		r.logger.Warn("audit emit failed",
			slog.String("plugin", slug),
			slog.String("event", eventType),
			slog.String("err", err.Error()))
		return err
	}
	return nil
}

// ===========================================================================
// Trap-event capture.
// ===========================================================================

// TrapEvent describes a single plugin trap captured for fan-out into
// the audit log (and — once #181's lifecycle counter ships — a
// Prometheus counter bump). Exported so external integration code
// (the lifecycle Manager, the dispatcher's trap path) can build one
// without reaching into the runtime package's internals.
type TrapEvent struct {
	Slug       string
	InstanceID string
	Reason     string
	Stack      string
	Fuel       float64
}

// ObservePluginTrap is called from the runtime trap-classification
// path (and from external integration code that wraps Module.Call)
// to record a plugin trap. The trap fans out to:
//
//   - The audit log as a `plugin.trap` event at warning severity,
//     carrying (slug, instance_id, reason, fuel_remaining, stack).
//   - The slog logger at error level so unhooked deployments still
//     see the trap in stdout.
//
// A Prometheus counter bump joins this path in the #181 commit;
// today the counter is gated on a *PluginMetrics that isn't wired
// in yet.
//
// ObservePluginTrap is goroutine-safe and never panics — a failing
// audit emit is logged and swallowed.
//
// Implementation note: we intentionally allocate the metadata map
// per-call rather than keeping a sync.Pool. Trap events are by
// definition rare, and the GC overhead is negligible compared to the
// trap-classification work that already happened upstream. A pool
// would add complexity for no measurable win.
func (r *Runtime) ObservePluginTrap(ctx context.Context, evt TrapEvent) {
	if evt.Slug == "" {
		return
	}
	meta := map[string]any{
		"reason":      evt.Reason,
		"instance_id": evt.InstanceID,
	}
	if evt.Fuel > 0 {
		meta["fuel_remaining"] = evt.Fuel
	}
	if evt.Stack != "" {
		meta["stack"] = evt.Stack
	}
	_ = emitObservabilityAudit(ctx, r, evt.Slug, "plugin.trap", "warning", meta)

	r.logger.Error("plugin trapped",
		slog.String("plugin", evt.Slug),
		slog.String("instance_id", evt.InstanceID),
		slog.String("reason", evt.Reason),
		slog.Float64("fuel_remaining", evt.Fuel))
}
