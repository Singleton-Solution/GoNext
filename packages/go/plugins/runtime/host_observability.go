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
	"fmt"
	"log/slog"
	"sync"
	"time"

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

// Tracer is the narrow OTel-tracing surface the runtime uses to
// wrap hook-bus dispatch and ABI calls. We define it as an interface
// rather than importing go.opentelemetry.io/otel/trace directly so:
//
//  1. The runtime package stays free of a hard OTel SDK dependency.
//     Hosts that don't run OTel pay zero cost.
//  2. Tests can supply a recording stub without spinning up the SDK.
//  3. The interface freezes the SHAPE we use even if upstream OTel
//     changes its API.
//
// Production wiring adapts otel.Tracer().Start to this interface
// (one-line wrapper). The SpanEventReceiver below covers the
// gn_span_event seam — separate from Tracer because span CREATION
// happens host-side, span EVENTS come from the guest.
//
// Implementations MUST be safe for concurrent use.
type Tracer interface {
	// StartSpan begins a span named `name` and returns a derived
	// context plus a closer. attrs is the initial attribute set
	// (gonext.plugin.slug, gonext.plugin.abi, ...). The returned
	// SpanCloser MUST be called exactly once when the span ends; it
	// records any error and finalises the span.
	StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, SpanCloser)
}

// SpanCloser is the cleanup half of a Tracer span. Implementations
// MUST be safe for concurrent use. Call End exactly once; calling
// SetAttribute / RecordError before End amends the span.
type SpanCloser interface {
	// End closes the span. err, if non-nil, is recorded as the span's
	// status. End is idempotent — additional calls after the first
	// are no-ops.
	End(err error)

	// SetAttribute amends the span with one (key, value) tag. Safe
	// to call between StartSpan and End. After End, this is a no-op.
	SetAttribute(key, value string)
}

// SpanEventReceiver is the seam the runtime uses to forward
// gn_span_event calls without taking a hard dependency on the OTel
// SDK. Production wiring adapts an OTel tracer; tests can supply a
// recording stub.
//
// Implementations MUST be safe for concurrent use.
type SpanEventReceiver interface {
	// AddSpanEvent attaches an event to whatever span is active for
	// (ctx, slug). Implementations with no active span SHOULD log the
	// event so plugin-emitted breadcrumbs aren't silently lost.
	AddSpanEvent(ctx context.Context, slug, name string, attrs map[string]string)
}

// observability is the per-Runtime sink bundle stored in the
// observabilityRegistry. Every field is nil-safe: the host functions
// branch on "no sink wired" before touching the field.
type observability struct {
	translator   i18n.Translator
	auditEmitter AuditEmitter
	metrics      *PluginMetrics
	tracer       Tracer
	spanReceiver SpanEventReceiver
	spanCtxs     *spanContextRegistry
	dam          *CardinalityDam
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
		spanCtxs:   newSpanContextRegistry(),
		dam:        NewCardinalityDam(DefaultCardinalityLimit),
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

// WithPluginMetrics installs the slug-bounded Prometheus catalogue.
// Passing nil disables every plugin metric. After installation, the
// runtime calls pm.RegisterSlug from LoadModule and pm.UnregisterSlug
// from Close — so a runtime with metrics wired in will refuse to
// load the 101st distinct plugin (the cardinality cap) and surface
// *ErrPluginSlugLimit.
func (b *ObservabilityBuilder) WithPluginMetrics(pm *PluginMetrics) *ObservabilityBuilder {
	mutateObs(b.rt, func(o *observability) { o.metrics = pm })
	return b
}

// WithTracer installs an OTel-style Tracer for hook and ABI span
// wrapping (#194). Passing nil disables tracing; the StartPluginSpan
// helper then becomes a no-op pass-through.
func (b *ObservabilityBuilder) WithTracer(t Tracer) *ObservabilityBuilder {
	mutateObs(b.rt, func(o *observability) { o.tracer = t })
	return b
}

// WithSpanEventReceiver installs the gn_span_event sink. Passing nil
// disables span propagation; gn_span_event then logs at debug level
// so plugin authors developing without an OTel pipeline still see
// their breadcrumbs.
func (b *ObservabilityBuilder) WithSpanEventReceiver(r SpanEventReceiver) *ObservabilityBuilder {
	mutateObs(b.rt, func(o *observability) { o.spanReceiver = r })
	return b
}

// WithCardinalityDam installs the per-plugin tag-value dam consulted
// by gn_metric_observe. Passing nil reverts to a fresh defaults-backed
// dam — the dam is load-bearing for Prometheus stability, so we never
// disable it entirely.
func (b *ObservabilityBuilder) WithCardinalityDam(d *CardinalityDam) *ObservabilityBuilder {
	mutateObs(b.rt, func(o *observability) {
		if d == nil {
			d = NewCardinalityDam(DefaultCardinalityLimit)
		}
		o.dam = d
	})
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

// PluginMetrics returns the runtime's metrics catalogue or nil when
// none has been wired in.
func (r *Runtime) PluginMetrics() *PluginMetrics { return obsFor(r).metrics }

// Tracer returns the configured Tracer or nil.
func (r *Runtime) Tracer() Tracer { return obsFor(r).tracer }

// SpanEventReceiver returns the configured span event receiver or nil.
func (r *Runtime) SpanEventReceiver() SpanEventReceiver { return obsFor(r).spanReceiver }

// CardinalityDam returns the runtime's per-plugin tag-value dam.
// Never nil after the first obsFor() — a runtime built without
// WithCardinalityDam gets a defaults-backed dam.
func (r *Runtime) CardinalityDam() *CardinalityDam { return obsFor(r).dam }

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

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(r.hostGnSpanEvent),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
			[]api.ValueType{api.ValueTypeI32}).
		WithParameterNames("name_ptr", "name_len", "attrs_ptr", "attrs_len").
		Export("gn_span_event")

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(r.hostGnMetricObserve),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeF64, api.ValueTypeI32, api.ValueTypeI32},
			[]api.ValueType{api.ValueTypeI32}).
		WithParameterNames("name_ptr", "name_len", "value", "tags_ptr", "tags_len").
		Export("gn_metric_observe")

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(r.hostGnEventEmit),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
			[]api.ValueType{api.ValueTypeI32}).
		WithParameterNames("name_ptr", "name_len", "data_ptr", "data_len").
		Export("gn_event_emit")
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

	start := time.Now()
	defer func() {
		o.metrics.ObserveABICallDuration(slug, "gn_i18n_translate", time.Since(start).Seconds())
	}()

	keyBuf, err := readHostString("gn_i18n_translate", mod, keyPtr, keyLen)
	if err != nil {
		r.logger.Warn("gn_i18n_translate: bad key args",
			slog.String("plugin", slug),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI64(0)
		o.metrics.IncABICall(slug, "gn_i18n_translate", "error")
		return
	}
	localeBuf, err := readHostString("gn_i18n_translate", mod, localePtr, localeLen)
	if err != nil {
		r.logger.Warn("gn_i18n_translate: bad locale args",
			slog.String("plugin", slug),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI64(0)
		o.metrics.IncABICall(slug, "gn_i18n_translate", "error")
		return
	}

	key := string(keyBuf)
	locale := string(localeBuf)
	translated := o.translator.Translate(key, locale)
	o.metrics.IncABICall(slug, "gn_i18n_translate", "ok")

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

// hostGnSpanEvent implements env.gn_span_event.
// Signature: (name_ptr i32, name_len i32, attrs_ptr i32, attrs_len i32) -> i32.
//
// Plugins use this to attach an event onto whatever OTel span the
// host is currently propagating for their call. The span itself is
// owned by the host (StartPluginSpan wraps every hook dispatch and
// ABI call); gn_span_event is the guest-side way to add named
// breadcrumbs inside that span.
//
// The attrs blob is a minimal-msgpack string-keyed string-valued
// map — same format the data ABI uses for filter args. On decode
// failure the host logs and returns statusBadTags; the guest call
// otherwise returns statusOK.
//
// When no SpanEventReceiver is wired in, the call falls back to a
// debug log so plugin authors developing without an OTel pipeline
// still see their breadcrumbs.
func (r *Runtime) hostGnSpanEvent(ctx context.Context, mod api.Module, stack []uint64) {
	namePtr := api.DecodeU32(stack[0])
	nameLen := api.DecodeU32(stack[1])
	attrsPtr := api.DecodeU32(stack[2])
	attrsLen := api.DecodeU32(stack[3])

	o := obsFor(r)
	slug := mod.Name()
	start := time.Now()
	defer func() {
		o.metrics.ObserveABICallDuration(slug, "gn_span_event", time.Since(start).Seconds())
	}()

	nameBuf, err := readHostString("gn_span_event", mod, namePtr, nameLen)
	if err != nil || len(nameBuf) == 0 {
		o.metrics.IncABICall(slug, "gn_span_event", "error")
		stack[0] = api.EncodeI32(statusBadName)
		return
	}
	eventName := string(nameBuf)

	attrs, err := readHostTags(mod, attrsPtr, attrsLen)
	if err != nil {
		r.logger.Warn("gn_span_event: bad attrs blob",
			slog.String("plugin", slug),
			slog.String("event", eventName),
			slog.String("err", err.Error()))
		o.metrics.IncABICall(slug, "gn_span_event", "error")
		stack[0] = api.EncodeI32(statusBadTags)
		return
	}

	if o.spanReceiver != nil {
		o.spanReceiver.AddSpanEvent(ctx, slug, eventName, attrs)
	} else {
		r.logger.Debug("plugin span event (no receiver wired)",
			slog.String("plugin", slug),
			slog.String("event", eventName),
			slog.Any("attrs", attrs))
	}
	o.metrics.IncABICall(slug, "gn_span_event", "ok")
	stack[0] = api.EncodeI32(statusOK)
}

// hostGnMetricObserve implements env.gn_metric_observe.
// Signature: (name_ptr i32, name_len i32, value f64, tags_ptr i32, tags_len i32) -> i32.
//
// The host reads the metric name and a msgpack-encoded tags map from
// guest memory, runs the (slug, metric, tags) tuple through the
// cardinality dam, and either records the observation or drops it
// with a `plugin.metric_cardinality_exceeded` audit warning + a
// Prometheus counter bump.
//
// The dam is the load-bearing safety net: without it a misbehaving
// plugin emitting `gn_metric_observe("requests", 1, {user_id: ...})`
// would explode Prometheus' series count. The audit event names the
// plugin and the offending tag so operators can react.
//
// Return statuses:
//
//	statusOK                    metric admitted
//	statusBadName               name empty or out of bounds
//	statusBadTags               tags blob unparseable
//	statusCardinalityExceeded   dam dropped the metric
func (r *Runtime) hostGnMetricObserve(ctx context.Context, mod api.Module, stack []uint64) {
	namePtr := api.DecodeU32(stack[0])
	nameLen := api.DecodeU32(stack[1])
	value := api.DecodeF64(stack[2])
	tagsPtr := api.DecodeU32(stack[3])
	tagsLen := api.DecodeU32(stack[4])

	o := obsFor(r)
	slug := mod.Name()
	start := time.Now()
	defer func() {
		o.metrics.ObserveABICallDuration(slug, "gn_metric_observe", time.Since(start).Seconds())
	}()

	nameBuf, err := readHostString("gn_metric_observe", mod, namePtr, nameLen)
	if err != nil || len(nameBuf) == 0 {
		o.metrics.IncABICall(slug, "gn_metric_observe", "error")
		stack[0] = api.EncodeI32(statusBadName)
		return
	}
	metricName := string(nameBuf)

	tags, err := readHostTags(mod, tagsPtr, tagsLen)
	if err != nil {
		r.logger.Warn("gn_metric_observe: bad tags blob",
			slog.String("plugin", slug),
			slog.String("metric", metricName),
			slog.String("err", err.Error()))
		o.metrics.IncABICall(slug, "gn_metric_observe", "error")
		stack[0] = api.EncodeI32(statusBadTags)
		return
	}

	// Cardinality check first — admit-or-drop. The dam is per-plugin,
	// so a noisy plugin can't damage other plugins' budgets.
	if overTag, admitted := o.dam.Admit(slug, metricName, tags); !admitted {
		o.metrics.IncABICall(slug, "gn_metric_observe", "cardinality_exceeded")
		o.metrics.IncMetricCardinalityExceeded(slug, metricName)
		_ = emitObservabilityAudit(ctx, r, slug, "plugin.metric_cardinality_exceeded", "warning", map[string]any{
			"metric":          metricName,
			"overflowing_tag": overTag,
			"limit":           o.dam.Limit(),
		})
		stack[0] = api.EncodeI32(statusCardinalityExceeded)
		return
	}

	// Forward to whatever downstream sink the host wired in. For now
	// the runtime owns no general-purpose plugin-metric sink; we
	// surface the observation via the slug-scoped Prometheus counters
	// (slug, metric tag set) and log at debug for visibility. A
	// follow-up commit can add a dedicated GaugeVec sink keyed by
	// (slug, metric); the dam already covers cardinality.
	r.logger.Debug("plugin metric observed",
		slog.String("plugin", slug),
		slog.String("metric", metricName),
		slog.Float64("value", value),
		slog.Any("tags", tags))
	o.metrics.IncABICall(slug, "gn_metric_observe", "ok")
	stack[0] = api.EncodeI32(statusOK)
}

// hostGnEventEmit implements env.gn_event_emit.
// Signature: (name_ptr i32, name_len i32, data_ptr i32, data_len i32) -> i32.
//
// Plugins call this to emit semi-structured "plugin event" rows into
// the audit log. The data blob is msgpack — same format as
// gn_metric_observe's tags — so a guest SDK that builds tags can
// reuse its encoder.
//
// Audit events are emitted at SeverityInfo. Plugins that want a
// higher severity must use the audit-specific ABI (lands separately).
func (r *Runtime) hostGnEventEmit(ctx context.Context, mod api.Module, stack []uint64) {
	namePtr := api.DecodeU32(stack[0])
	nameLen := api.DecodeU32(stack[1])
	dataPtr := api.DecodeU32(stack[2])
	dataLen := api.DecodeU32(stack[3])

	o := obsFor(r)
	slug := mod.Name()
	start := time.Now()
	defer func() {
		o.metrics.ObserveABICallDuration(slug, "gn_event_emit", time.Since(start).Seconds())
	}()

	nameBuf, err := readHostString("gn_event_emit", mod, namePtr, nameLen)
	if err != nil || len(nameBuf) == 0 {
		o.metrics.IncABICall(slug, "gn_event_emit", "error")
		stack[0] = api.EncodeI32(statusBadName)
		return
	}
	eventName := string(nameBuf)

	data, err := readHostTags(mod, dataPtr, dataLen)
	if err != nil {
		r.logger.Warn("gn_event_emit: bad data blob",
			slog.String("plugin", slug),
			slog.String("event", eventName),
			slog.String("err", err.Error()))
		o.metrics.IncABICall(slug, "gn_event_emit", "error")
		stack[0] = api.EncodeI32(statusBadTags)
		return
	}

	meta := make(map[string]any, len(data))
	for k, v := range data {
		meta[k] = v
	}
	if err := emitObservabilityAudit(ctx, r, slug, eventName, "info", meta); err != nil {
		o.metrics.IncABICall(slug, "gn_event_emit", "error")
		stack[0] = api.EncodeI32(statusBackendUnavailable)
		return
	}
	o.metrics.IncABICall(slug, "gn_event_emit", "ok")
	stack[0] = api.EncodeI32(statusOK)
}

// ===========================================================================
// Span wrapper for hook dispatch and ABI calls (#194).
// ===========================================================================

// StartPluginSpan begins a span for one plugin operation — a hook
// dispatch or an ABI call — and returns the derived context plus a
// closer the caller must invoke on the way out.
//
// Attributes:
//
//	gonext.plugin.slug   — the plugin's manifest slug
//	gonext.plugin.abi    — the host function or hook name being run
//	(amend with fuel_consumed via spanCloser.SetAttribute on exit)
//
// The dispatcher wraps each hook-bus call this way; the runtime
// wraps each ABI call. Tracer can be nil (no OTel configured) — the
// returned closer is a no-op and ctx is the input ctx.
//
// The function records the active span context on the per-Module
// registry so any gn_span_event the guest fires can attach to the
// same span — even though wazero strips arbitrary ctx values on the
// guest hop. End() clears the registry entry.
func (r *Runtime) StartPluginSpan(ctx context.Context, slug, abi string) (context.Context, SpanCloser) {
	o := obsFor(r)
	if o.tracer == nil {
		return ctx, noopSpanCloser{}
	}
	attrs := map[string]string{
		"gonext.plugin.slug": slug,
		"gonext.plugin.abi":  abi,
	}
	spanCtx, closer := o.tracer.StartSpan(ctx, "plugin."+abi, attrs)
	// Record the span context against the module so gn_span_event
	// can correlate. We use a string key derived from slug+abi rather
	// than the raw module name because a single module can be in
	// multiple concurrent ABI calls; the (slug, abi) tuple is the
	// finest-grain correlation we have without bolting an instance
	// id into the runtime.
	if o.spanCtxs != nil {
		o.spanCtxs.markActive(slug, abi)
	}
	wrapped := &trackedSpanCloser{
		SpanCloser: closer,
		runtime:    r,
		slug:       slug,
		abi:        abi,
	}
	return spanCtx, wrapped
}

// trackedSpanCloser wraps a Tracer-returned SpanCloser so we can
// clear the active-span registry on End. The wrapping is needed
// because the runtime tracks "is there a live span for this module?"
// independent of OTel — the tracker is what gn_span_event consults
// to decide whether to forward to the SpanEventReceiver or log.
type trackedSpanCloser struct {
	SpanCloser
	runtime *Runtime
	slug    string
	abi     string
	done    sync.Once
}

func (c *trackedSpanCloser) End(err error) {
	c.done.Do(func() {
		c.SpanCloser.End(err)
		if o := obsFor(c.runtime); o.spanCtxs != nil {
			o.spanCtxs.clearActive(c.slug, c.abi)
		}
	})
}

// noopSpanCloser is the SpanCloser returned when no Tracer is wired
// in. Every method is a no-op so callers can chain unconditionally.
type noopSpanCloser struct{}

func (noopSpanCloser) End(error)              {}
func (noopSpanCloser) SetAttribute(_, _ string) {}

// ===========================================================================
// Status return codes shared by the gn_span_event ABI (and, in
// follow-up commits, gn_metric_observe and gn_event_emit). 0 is
// success; the rest are negative sentinels so a successful
// drop-through can never be misread as an error.
// ===========================================================================

const (
	statusOK                  int32 = 0
	statusBadName             int32 = -1
	statusBadTags             int32 = -3
	statusCardinalityExceeded int32 = -4
	statusBackendUnavailable  int32 = -5
)

// ===========================================================================
// Helpers.
// ===========================================================================

// readHostTags reads a tag/attribute blob out of guest memory and
// decodes it as a flat string-keyed string-valued map.
//
// The wire format is a minimal subset of msgpack that the runtime
// supports without dragging in a full msgpack dependency: a fixmap
// (or map16) whose keys and values are all fixstr (or str8/str16).
// This is the exact subset the plugin SDKs emit; richer types fall
// back to fmt-style stringification at the SDK boundary.
//
// On any decode failure the call returns the error; callers surface
// it as statusBadTags. nil/zero-length input decodes to an empty map.
func readHostTags(mod api.Module, ptr, length uint32) (map[string]string, error) {
	if length == 0 {
		return map[string]string{}, nil
	}
	buf, err := readHostString("readHostTags", mod, ptr, length)
	if err != nil {
		return nil, err
	}
	return decodeStringMap(buf)
}

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
	o := obsFor(r)
	o.metrics.IncLifecycle(evt.Slug, "trap")

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

// ===========================================================================
// Lifecycle hooks (#181). These are called by external integration
// code (the lifecycle Manager) on plugin load / close so the metric
// catalogue stays in sync with what's actually running. We don't bake
// the calls into Runtime.LoadModule / Module.Close to keep this PR
// from touching the central runtime.go — parallel ABI agents would
// then have to rebase through every observability change.
// ===========================================================================

// RegisterPluginSlug admits slug into the cardinality-bounded metric
// catalogue and bumps the create-event lifecycle counter. The two
// happen together so a caller that successfully registers always sees
// the corresponding create counter increment.
//
// Returns *ErrPluginSlugLimit when the slug cap is hit; the caller is
// expected to surface the failure as a clean "plugin temporarily
// unavailable" condition to the operator. The slug is NOT loaded into
// the runtime in that case.
//
// Calling this for a slug that's already admitted is a no-op and
// returns nil — registration is idempotent. The lifecycle counter
// still bumps, which is intentional: a fresh "create" event reflects
// the new instance even though the slug was admitted earlier.
func (r *Runtime) RegisterPluginSlug(slug string) error {
	o := obsFor(r)
	if o.metrics == nil {
		// No metrics catalogue wired in — nothing to gate. The lifecycle
		// counter is also a no-op. Return nil so callers can still call
		// this unconditionally.
		return nil
	}
	if err := o.metrics.RegisterSlug(slug); err != nil {
		return err
	}
	o.metrics.IncLifecycle(slug, "create")
	return nil
}

// UnregisterPluginSlug drops slug from the cardinality set and bumps
// the destroy-event lifecycle counter. Idempotent.
func (r *Runtime) UnregisterPluginSlug(slug string) {
	o := obsFor(r)
	if o.metrics == nil {
		return
	}
	o.metrics.IncLifecycle(slug, "destroy")
	o.metrics.UnregisterSlug(slug)
}

// RecordPluginTimeout bumps the timeout counter for (slug, abi).
// External integration code calls this from the per-call deadline
// path when the enforcer's hard cancel fires. abi is the host
// function the call was inside when the deadline tripped, or empty
// to attribute against the outer Module.Call envelope.
func (r *Runtime) RecordPluginTimeout(slug, abi string) {
	obsFor(r).metrics.IncTimeout(slug, abi)
}

// RecordPluginFuel adds fuel units burned by slug. Bumped from the
// per-call deadline integration once a fuel meter ships; the seam is
// here so the lifecycle Manager has a single place to call.
func (r *Runtime) RecordPluginFuel(slug string, fuel float64) {
	obsFor(r).metrics.IncFuel(slug, fuel)
}

// ===========================================================================
// Tiny msgpack decoder (string -> string maps only).
// ===========================================================================

// decodeStringMap parses a msgpack-encoded map whose keys and values
// are all strings, into a Go map. Supports:
//
//   - fixmap   (0x80..0x8f)               — count in low 4 bits
//   - map16    (0xde)                     — count in next 2 bytes BE
//   - map32    (0xdf)                     — count in next 4 bytes BE
//   - fixstr   (0xa0..0xbf)               — len in low 5 bits
//   - str8     (0xd9)                     — len in next 1 byte
//   - str16    (0xda)                     — len in next 2 bytes BE
//   - str32    (0xdb)                     — len in next 4 bytes BE
//
// Anything else returns an error. We deliberately don't pull in a full
// msgpack library — the runtime's wire surface is tiny and bringing in
// a generic codec would bloat the binary and the ABI test matrix.
//
// To keep accidental abuse bounded, the decoder rejects any single
// string longer than maxHostStringLen.
func decodeStringMap(buf []byte) (map[string]string, error) {
	d := &mpDecoder{buf: buf}
	count, err := d.readMapHeader()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, count)
	for i := uint32(0); i < count; i++ {
		k, err := d.readString()
		if err != nil {
			return nil, fmt.Errorf("key %d: %w", i, err)
		}
		v, err := d.readString()
		if err != nil {
			return nil, fmt.Errorf("value %d: %w", i, err)
		}
		out[k] = v
	}
	return out, nil
}

type mpDecoder struct {
	buf []byte
	pos int
}

func (d *mpDecoder) need(n int) error {
	if d.pos+n > len(d.buf) {
		return fmt.Errorf("msgpack: need %d bytes at pos %d, have %d", n, d.pos, len(d.buf)-d.pos)
	}
	return nil
}

func (d *mpDecoder) readMapHeader() (uint32, error) {
	if err := d.need(1); err != nil {
		return 0, err
	}
	b := d.buf[d.pos]
	d.pos++
	switch {
	case b >= 0x80 && b <= 0x8f:
		return uint32(b & 0x0f), nil
	case b == 0xde:
		if err := d.need(2); err != nil {
			return 0, err
		}
		n := uint32(d.buf[d.pos])<<8 | uint32(d.buf[d.pos+1])
		d.pos += 2
		return n, nil
	case b == 0xdf:
		if err := d.need(4); err != nil {
			return 0, err
		}
		n := uint32(d.buf[d.pos])<<24 | uint32(d.buf[d.pos+1])<<16 |
			uint32(d.buf[d.pos+2])<<8 | uint32(d.buf[d.pos+3])
		d.pos += 4
		return n, nil
	default:
		return 0, fmt.Errorf("msgpack: expected map header, got 0x%02x", b)
	}
}

func (d *mpDecoder) readString() (string, error) {
	if err := d.need(1); err != nil {
		return "", err
	}
	b := d.buf[d.pos]
	d.pos++
	var length uint32
	switch {
	case b >= 0xa0 && b <= 0xbf:
		length = uint32(b & 0x1f)
	case b == 0xd9:
		if err := d.need(1); err != nil {
			return "", err
		}
		length = uint32(d.buf[d.pos])
		d.pos++
	case b == 0xda:
		if err := d.need(2); err != nil {
			return "", err
		}
		length = uint32(d.buf[d.pos])<<8 | uint32(d.buf[d.pos+1])
		d.pos += 2
	case b == 0xdb:
		if err := d.need(4); err != nil {
			return "", err
		}
		length = uint32(d.buf[d.pos])<<24 | uint32(d.buf[d.pos+1])<<16 |
			uint32(d.buf[d.pos+2])<<8 | uint32(d.buf[d.pos+3])
		d.pos += 4
	default:
		return "", fmt.Errorf("msgpack: expected string header, got 0x%02x", b)
	}
	if length > maxHostStringLen {
		return "", fmt.Errorf("msgpack: string length %d exceeds host cap %d", length, maxHostStringLen)
	}
	if err := d.need(int(length)); err != nil {
		return "", err
	}
	s := string(d.buf[d.pos : d.pos+int(length)])
	d.pos += int(length)
	return s, nil
}

// EncodeStringMap is the inverse of decodeStringMap, exposed for tests
// and tooling that build the msgpack blobs guests would emit. The
// production guest SDKs have their own msgpack encoder; this function
// is the host-side fixture builder.
//
// Always emits map16 / str16 prefixes to keep the encoding code one
// branch deep. The resulting blob is larger than a tight fixmap but
// the decoder accepts both forms.
func EncodeStringMap(m map[string]string) []byte {
	out := make([]byte, 0, 64)
	out = append(out, 0xde, byte(len(m)>>8), byte(len(m)))
	for k, v := range m {
		out = append(out, 0xda, byte(len(k)>>8), byte(len(k)))
		out = append(out, k...)
		out = append(out, 0xda, byte(len(v)>>8), byte(len(v)))
		out = append(out, v...)
	}
	return out
}

// ===========================================================================
// Span propagation context state.
// ===========================================================================

// spanContextRegistry threads OTel span state across the WASM
// boundary. We hang the live-span set off this registry rather than
// ctx (which wazero strips of arbitrary values on its way into the
// guest).
//
// The set is per-(slug, abi) — a single plugin can be inside multiple
// concurrent ABI calls, each carrying its own span. The registry
// tracks "is there a host-side span live for this tuple?" so
// gn_span_event can decide whether to forward to the SpanEventReceiver
// or fall back to a debug log.
type spanContextRegistry struct {
	mu     sync.Mutex
	active map[string]int // key="slug|abi", value=ref count for nested calls
}

func newSpanContextRegistry() *spanContextRegistry {
	return &spanContextRegistry{active: make(map[string]int)}
}

func spanKey(slug, abi string) string { return slug + "|" + abi }

func (s *spanContextRegistry) markActive(slug, abi string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[spanKey(slug, abi)]++
}

func (s *spanContextRegistry) clearActive(slug, abi string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := spanKey(slug, abi)
	s.active[k]--
	if s.active[k] <= 0 {
		delete(s.active, k)
	}
}

// IsSpanActive reports whether StartPluginSpan has an open span for
// (slug, abi). Exposed for tests and the gn_span_event fast path —
// the production receiver typically reaches into ctx for the real
// span, but the registry is a cheap "is there anything to attach to?"
// signal.
func (r *Runtime) IsSpanActive(slug, abi string) bool {
	o := obsFor(r)
	if o.spanCtxs == nil {
		return false
	}
	o.spanCtxs.mu.Lock()
	defer o.spanCtxs.mu.Unlock()
	return o.spanCtxs.active[spanKey(slug, abi)] > 0
}
