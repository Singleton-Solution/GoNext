package runtime

// host_platform.go wires the plugin platform ABIs onto the wazero
// runtime: gn_secrets_get (#114, #166), gn_audit_emit (#183), and
// gn_cron_register (#191). The service-layer implementations live in
// platform_secrets.go, platform_audit.go, and platform_cron.go; this
// file is the thin adapter between wazero's (ptr, len) calling
// convention and the typed Go services.
//
// The file is deliberately separate from host.go so concurrent ABI
// work (capability ABI #107, data ABI, network ABI, observability ABI)
// doesn't collide on the same file during merges.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// maxSecretValueBytes caps the plaintext bytes we'll write back into
// guest memory for a single gn_secrets_get call. 64 KiB matches
// maxHostStringLen — the host-side caps for read and write paths stay
// in sync so a guest sizing buffers symmetrically gets matching
// behaviour on both sides of the boundary.
const maxSecretValueBytes = 64 * 1024

// AuditEmitterFunc is the minimum sink shape the platform layer
// requires. audit.Emitter satisfies this; declaring an interface here
// keeps runtime free of a hard audit-package dependency, which keeps
// the cross-package test surface tiny.
type AuditEmitterFunc interface {
	Emit(ctx context.Context, pluginSlug, eventType string, metadata map[string]any) error
}

// platformContext bundles the runtime-level dependencies the platform
// host functions need. One per Runtime; constructed by WithPlatform()
// and attached during New().
//
// Each field is independently nil-able. A Runtime that wires only the
// SecretsService gets only gn_secrets_get; a Runtime that wires nothing
// gets no platform exports at all. Plugins importing an unconfigured
// export fail at module instantiation with wazero's standard "missing
// import" error — the correct loud-failure mode for a misconfigured
// host.
type platformContext struct {
	secrets *SecretsService

	// audit is the rate-limited, slug-gated sink backing
	// gn_audit_emit (#183). Nil when the host did not configure
	// audit; the export is then absent.
	audit *AuditSink

	// cron is the persistence + dispatch seam backing
	// gn_cron_register (#191). Nil when the host did not configure
	// cron; the export is then absent.
	cron *CronService

	// slugFor maps a wazero module name to the plugin's slug. Nil is
	// fine when the host uses "module name == slug" (the documented
	// convention). A deployment that picks instance-unique module
	// names (one per pool entry) supplies a real mapper.
	slugFor func(moduleName string) string

	// platformEmitter records the runtime's own "plugin.<slug>.platform.*"
	// audit events for each platform call. Separate from the guest-
	// facing audit sink (audit) because these rows bypass the slug-
	// prefix gate — the platform owns the event name, not the guest.
	// Nil is permitted; platform events simply don't get written.
	platformEmitter AuditEmitterFunc
}

// resolveSlug returns the plugin slug for the wazero module currently
// executing. Defaults to moduleName when no mapper is registered.
func (p *platformContext) resolveSlug(moduleName string) string {
	if p == nil {
		return moduleName
	}
	if p.slugFor != nil {
		if s := p.slugFor(moduleName); s != "" {
			return s
		}
	}
	return moduleName
}

// emitPlatform writes a platform-internal audit event. Errors are
// logged at warn — we never let an audit-emit failure trap a guest,
// because the resulting "kill the plugin by killing the audit DB"
// surface is worse than a single missing audit row.
func (p *platformContext) emitPlatform(ctx context.Context, log *slog.Logger, pluginSlug, event string, meta map[string]any) {
	if p == nil || p.platformEmitter == nil {
		return
	}
	if err := p.platformEmitter.Emit(ctx, pluginSlug, event, meta); err != nil {
		log.WarnContext(ctx, "runtime: platform audit emit failed",
			slog.String("event", event),
			slog.String("plugin", pluginSlug),
			slog.String("err", err.Error()))
	}
}

// PlatformConfig bundles the dependencies WithPlatform consumes. One
// per process, constructed at boot.
//
// Secrets / Audit / Cron are independently optional. A host that wants
// to expose just one ABI (e.g. during early rollout) leaves the others
// nil. Guests importing an unconfigured export fail at module
// instantiation.
//
// PlatformEmitter sinks platform-internal audit rows. Nil is permitted
// in test environments; production hosts are expected to supply one.
//
// SlugFor maps a wazero module name to a plugin slug. Nil is fine
// when the host uses "module name == slug" (the documented convention).
type PlatformConfig struct {
	Secrets *SecretsService

	// Audit is the per-plugin slug-gated rate-limited audit emitter
	// backing gn_audit_emit (#183). May be nil.
	Audit *AuditSink

	// Cron is the schedule-register seam backing gn_cron_register
	// (#191). May be nil.
	Cron *CronService

	PlatformEmitter AuditEmitterFunc
	SlugFor         func(moduleName string) string
}

// WithPlatform installs the platform ABIs onto the runtime. Without
// this option, none of the platform exports are registered.
//
// The platform exports land in the "env" namespace alongside the
// built-in gn_log / gn_panic / gn_time_ms exports — guests find every
// gn_* import under one name, which matches what every WASM toolchain
// emits by default. A WithHostModule builder that also writes to "env"
// MUST avoid name collisions with the platform exports; wazero
// surfaces a collision as a name-conflict error at instantiation.
func WithPlatform(cfg PlatformConfig) Option {
	return func(rc *runtimeConfig) {
		rc.platform = &platformContext{
			secrets:         cfg.Secrets,
			audit:           cfg.Audit,
			cron:            cfg.Cron,
			slugFor:         cfg.SlugFor,
			platformEmitter: cfg.PlatformEmitter,
		}
	}
}

// addPlatformExports adds the configured platform exports onto the
// supplied wazero HostModuleBuilder. Called from registerEnvHost so
// the platform and built-in exports share one Instantiate against the
// env namespace.
//
// Which exports are added depends on which platformContext fields are
// non-nil: a Runtime without a SecretsService gets no gn_secrets_get.
func (r *Runtime) addPlatformExports(b wazero.HostModuleBuilder) {
	if r.platform == nil {
		return
	}
	if r.platform.secrets != nil {
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(r.hostGnSecretsGet),
				[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
				[]api.ValueType{api.ValueTypeI32}).
			WithParameterNames("key_ptr", "key_len", "out_ptr", "out_cap").
			Export("gn_secrets_get")
	}
	if r.platform.audit != nil {
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(r.hostGnAuditEmit),
				[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
				[]api.ValueType{api.ValueTypeI32}).
			WithParameterNames("event_ptr", "event_len", "meta_ptr", "meta_len").
			Export("gn_audit_emit")
	}
	if r.platform.cron != nil {
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(r.hostGnCronRegister),
				[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
				[]api.ValueType{api.ValueTypeI32}).
			WithParameterNames("schedule_ptr", "schedule_len", "handler_ptr", "handler_len").
			Export("gn_cron_register")
	}
}

// ----- gn_secrets_get ------------------------------------------------

// hostGnSecretsGet implements env.gn_secrets_get.
//
// Signature: (key_ptr i32, key_len i32, out_ptr i32, out_cap i32) -> i32
//
// Returns:
//
//   * >= 0: number of plaintext bytes written into out_ptr. If the
//     returned value EXCEEDS out_cap, the host did NOT write any
//     bytes (size probe). The guest can re-allocate and call again.
//   * -1:   error (not found, decrypt failure, service not configured,
//     OOB pointer, oversized value).
//
// Every call emits a plugin.<slug>.platform.secrets.get audit row
// with the (key, result) pair so an operator sees what plugins are
// asking for — whether or not the call succeeded.
//
// The plaintext slice is zeroed via defer after the write so the host
// heap doesn't retain it longer than necessary.
func (r *Runtime) hostGnSecretsGet(ctx context.Context, mod api.Module, stack []uint64) {
	keyPtr := api.DecodeU32(stack[0])
	keyLen := api.DecodeU32(stack[1])
	outPtr := api.DecodeU32(stack[2])
	outCap := api.DecodeU32(stack[3])

	if r.platform == nil || r.platform.secrets == nil {
		r.logger.WarnContext(ctx, "runtime: gn_secrets_get: service not configured",
			slog.String("module", mod.Name()))
		stack[0] = api.EncodeI32(-1)
		return
	}

	keyBuf, err := readHostString("gn_secrets_get", mod, keyPtr, keyLen)
	if err != nil {
		r.logger.WarnContext(ctx, "runtime: gn_secrets_get: bad key args",
			slog.String("module", mod.Name()),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI32(-1)
		return
	}
	// Copy out of guest memory: the store/decrypt path may take a
	// while and the original slice could be invalidated by a
	// concurrent guest allocation.
	key := string(append([]byte(nil), keyBuf...))

	pluginSlug := r.platform.resolveSlug(mod.Name())
	plain, err := r.platform.secrets.Get(ctx, pluginSlug, key)

	auditMeta := map[string]any{"key": key}
	auditEvent := "plugin." + pluginSlug + ".platform.secrets.get"
	if err != nil {
		switch {
		case errors.Is(err, ErrSecretNotFound):
			auditMeta["result"] = "not_found"
		case errors.Is(err, ErrSecretDecrypt):
			auditMeta["result"] = "decrypt_failed"
		default:
			auditMeta["result"] = "error"
		}
		r.platform.emitPlatform(ctx, r.logger, pluginSlug, auditEvent, auditMeta)
		r.logger.WarnContext(ctx, "runtime: gn_secrets_get: failed",
			slog.String("module", mod.Name()),
			slog.String("plugin", pluginSlug),
			slog.String("key", key),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI32(-1)
		return
	}
	defer zero(plain)

	if len(plain) > maxSecretValueBytes {
		r.logger.WarnContext(ctx, "runtime: gn_secrets_get: plaintext exceeds host cap",
			slog.String("module", mod.Name()),
			slog.String("plugin", pluginSlug),
			slog.Int("size", len(plain)))
		stack[0] = api.EncodeI32(-1)
		return
	}

	// Size-probe path: if out_cap is too small, return the required
	// size without writing. Guest re-allocs and calls again.
	if uint32(len(plain)) > outCap {
		auditMeta["result"] = "size_probe"
		auditMeta["size"] = len(plain)
		r.platform.emitPlatform(ctx, r.logger, pluginSlug, auditEvent, auditMeta)
		stack[0] = api.EncodeI32(int32(len(plain)))
		return
	}

	mem := mod.Memory()
	if mem == nil {
		r.logger.WarnContext(ctx, "runtime: gn_secrets_get: no memory",
			slog.String("module", mod.Name()))
		stack[0] = api.EncodeI32(-1)
		return
	}
	if !mem.Write(outPtr, plain) {
		r.logger.WarnContext(ctx, "runtime: gn_secrets_get: out_ptr OOB",
			slog.String("module", mod.Name()),
			slog.Uint64("out_ptr", uint64(outPtr)),
			slog.Uint64("size", uint64(len(plain))),
			slog.Uint64("mem_size", uint64(mem.Size())))
		stack[0] = api.EncodeI32(-1)
		return
	}

	auditMeta["result"] = "ok"
	auditMeta["size"] = len(plain)
	r.platform.emitPlatform(ctx, r.logger, pluginSlug, auditEvent, auditMeta)
	stack[0] = api.EncodeI32(int32(len(plain)))
}

// maxAuditMetadataBytes caps the JSON payload a guest can hand to
// gn_audit_emit. 32 KiB is generous for structured event metadata
// (the typical audit row metadata is a few hundred bytes); larger
// payloads almost always indicate a guest bug shoveling unbounded
// data into the audit log.
const maxAuditMetadataBytes = 32 * 1024

// ----- gn_audit_emit -------------------------------------------------

// hostGnAuditEmit implements env.gn_audit_emit.
//
// Signature: (event_ptr i32, event_len i32, meta_ptr i32, meta_len i32) -> i32
//
// Returns 0 on success, -1 on any rejection. Rejection reasons:
//
//   * event_name doesn't start with the plugin's slug
//     (ErrAuditSlugPrefix)
//   * Per-plugin rate-limit exceeded (ErrAuditRateLimited)
//   * meta payload is malformed JSON or exceeds maxAuditMetadataBytes
//   * Service not configured
//
// Every call (success or rejection) emits a
// plugin.<slug>.platform.audit_emit row tracking the attempt, so an
// operator sees what plugins are doing even when rate-limited.
func (r *Runtime) hostGnAuditEmit(ctx context.Context, mod api.Module, stack []uint64) {
	eventPtr := api.DecodeU32(stack[0])
	eventLen := api.DecodeU32(stack[1])
	metaPtr := api.DecodeU32(stack[2])
	metaLen := api.DecodeU32(stack[3])

	if r.platform == nil || r.platform.audit == nil {
		r.logger.WarnContext(ctx, "runtime: gn_audit_emit: service not configured",
			slog.String("module", mod.Name()))
		stack[0] = api.EncodeI32(-1)
		return
	}

	pluginSlug := r.platform.resolveSlug(mod.Name())

	eventBuf, err := readHostString("gn_audit_emit", mod, eventPtr, eventLen)
	if err != nil {
		r.logger.WarnContext(ctx, "runtime: gn_audit_emit: bad event args",
			slog.String("module", mod.Name()),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI32(-1)
		return
	}
	eventName := string(append([]byte(nil), eventBuf...))

	if metaLen > maxAuditMetadataBytes {
		r.logger.WarnContext(ctx, "runtime: gn_audit_emit: metadata too large",
			slog.String("module", mod.Name()),
			slog.Uint64("size", uint64(metaLen)))
		stack[0] = api.EncodeI32(-1)
		return
	}
	var metadata map[string]any
	if metaLen > 0 {
		metaBuf, err := readHostString("gn_audit_emit", mod, metaPtr, metaLen)
		if err != nil {
			r.logger.WarnContext(ctx, "runtime: gn_audit_emit: bad meta args",
				slog.String("module", mod.Name()),
				slog.String("err", err.Error()))
			stack[0] = api.EncodeI32(-1)
			return
		}
		if err := json.Unmarshal(metaBuf, &metadata); err != nil {
			r.logger.WarnContext(ctx, "runtime: gn_audit_emit: invalid JSON",
				slog.String("module", mod.Name()),
				slog.String("err", err.Error()))
			stack[0] = api.EncodeI32(-1)
			return
		}
	}

	platformMeta := map[string]any{"event": eventName}
	platformEvent := "plugin." + pluginSlug + ".platform.audit_emit"
	if err := r.platform.audit.Emit(ctx, pluginSlug, eventName, metadata); err != nil {
		switch {
		case errors.Is(err, ErrAuditSlugPrefix):
			platformMeta["result"] = "slug_prefix_rejected"
		case errors.Is(err, ErrAuditRateLimited):
			platformMeta["result"] = "rate_limited"
		case errors.Is(err, ErrAuditEmpty):
			platformMeta["result"] = "empty_event"
		default:
			platformMeta["result"] = "error"
		}
		r.platform.emitPlatform(ctx, r.logger, pluginSlug, platformEvent, platformMeta)
		r.logger.WarnContext(ctx, "runtime: gn_audit_emit: rejected",
			slog.String("module", mod.Name()),
			slog.String("plugin", pluginSlug),
			slog.String("event", eventName),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI32(-1)
		return
	}

	platformMeta["result"] = "ok"
	r.platform.emitPlatform(ctx, r.logger, pluginSlug, platformEvent, platformMeta)
	stack[0] = api.EncodeI32(0)
}

// ----- gn_cron_register ----------------------------------------------

// hostGnCronRegister implements env.gn_cron_register.
//
// Signature: (schedule_ptr i32, schedule_len i32, handler_ptr i32, handler_len i32) -> i32
//
// Returns 0 on success, -1 on rejection (empty/missing args,
// bad handler ID shape, service not configured, store failure).
//
// Each call emits a plugin.<slug>.platform.cron_register row with the
// (schedule, handler_id, result) triple so an operator can see
// exactly what schedules a plugin asked for during activation. The
// audit row is written even on rejection so misbehaving plugins are
// visible in the audit trail.
func (r *Runtime) hostGnCronRegister(ctx context.Context, mod api.Module, stack []uint64) {
	schedPtr := api.DecodeU32(stack[0])
	schedLen := api.DecodeU32(stack[1])
	handlerPtr := api.DecodeU32(stack[2])
	handlerLen := api.DecodeU32(stack[3])

	if r.platform == nil || r.platform.cron == nil {
		r.logger.WarnContext(ctx, "runtime: gn_cron_register: service not configured",
			slog.String("module", mod.Name()))
		stack[0] = api.EncodeI32(-1)
		return
	}

	pluginSlug := r.platform.resolveSlug(mod.Name())

	schedBuf, err := readHostString("gn_cron_register", mod, schedPtr, schedLen)
	if err != nil {
		r.logger.WarnContext(ctx, "runtime: gn_cron_register: bad schedule args",
			slog.String("module", mod.Name()),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI32(-1)
		return
	}
	schedule := string(append([]byte(nil), schedBuf...))

	handlerBuf, err := readHostString("gn_cron_register", mod, handlerPtr, handlerLen)
	if err != nil {
		r.logger.WarnContext(ctx, "runtime: gn_cron_register: bad handler args",
			slog.String("module", mod.Name()),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI32(-1)
		return
	}
	handlerID := string(append([]byte(nil), handlerBuf...))

	auditMeta := map[string]any{
		"schedule":   schedule,
		"handler_id": handlerID,
	}
	auditEvent := "plugin." + pluginSlug + ".platform.cron_register"
	if err := r.platform.cron.Register(ctx, pluginSlug, schedule, handlerID); err != nil {
		switch {
		case errors.Is(err, ErrCronEmpty):
			auditMeta["result"] = "empty"
		case errors.Is(err, ErrCronHandlerIDShape):
			auditMeta["result"] = "bad_handler_id"
		default:
			auditMeta["result"] = "error"
		}
		r.platform.emitPlatform(ctx, r.logger, pluginSlug, auditEvent, auditMeta)
		r.logger.WarnContext(ctx, "runtime: gn_cron_register: rejected",
			slog.String("module", mod.Name()),
			slog.String("plugin", pluginSlug),
			slog.String("schedule", schedule),
			slog.String("handler_id", handlerID),
			slog.String("err", err.Error()))
		stack[0] = api.EncodeI32(-1)
		return
	}

	auditMeta["result"] = "ok"
	r.platform.emitPlatform(ctx, r.logger, pluginSlug, auditEvent, auditMeta)
	stack[0] = api.EncodeI32(0)
}

