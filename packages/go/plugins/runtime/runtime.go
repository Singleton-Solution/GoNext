package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/plugins/runtime/limits"
	"github.com/tetratelabs/wazero"
)

// defaultMemoryLimitPages is the per-module hard cap on linear memory,
// in 64 KiB pages. 256 pages = 16 MiB.
//
// This is a placeholder until the real per-plugin resource limits land
// in issue #15 (where the limit comes from the plugin manifest and is
// enforced both at instantiation and via a fuel meter). 16 MiB is a
// generous baseline — most plugin workloads we expect (data
// transforms, content rendering, validation hooks) sit well under that.
const defaultMemoryLimitPages uint32 = 256

// hostModuleName is the namespace under which the runtime registers
// host functions (gn_log, gn_panic, gn_time_ms). "env" is the
// convention every mainstream WASM toolchain (Rust, AssemblyScript,
// TinyGo) emits imports under by default; using it means plugin
// authors don't need any custom import-name mapping.
const hostModuleName = "env"

// TimeSource is the abstraction over time.Now used by the gn_time_ms
// host function. Tests inject a fixed source; production code uses
// time.Now.
type TimeSource func() time.Time

// LogPublisher is the seam the debug package (and any future
// log-sink) uses to receive gn_log events as they happen. Each
// gn_log call from any module triggers exactly one Publish, with the
// runtime supplying the module name, the int32 level the guest passed,
// and the decoded message string.
//
// Implementations MUST be non-blocking: this is called from the wazero
// host-function thread, and any back-pressure would stall every plugin
// that logs. The reference implementation (debug.LogHub) drops on a
// full subscriber buffer rather than waiting.
//
// Publish runs on whichever goroutine called Module.Call; nothing in
// the runtime serialises it.
type LogPublisher interface {
	Publish(module string, level int32, message string)
}

// Runtime is the wazero-backed WebAssembly host.
//
// One Runtime per process is the intended pattern — it owns the wazero
// runtime, the compiled host functions, and the slot map of active
// modules. LoadModule returns a Module wrapped around a fresh wazero
// instance.
//
// Runtime is goroutine-safe: LoadModule can be called from any
// goroutine, and the active-modules map is guarded by a mutex.
type Runtime struct {
	// wazeroRT is the underlying wazero runtime. All compiled modules
	// and instantiated modules go through it.
	wazeroRT wazero.Runtime

	// logger is the structured logger for non-trap diagnostics. Trap
	// information is returned as *TrapError; this logger is only used
	// for warnings (failed close, failed host write) that the caller
	// can't easily surface.
	logger *slog.Logger

	// timeSource backs the gn_time_ms host function.
	timeSource TimeSource

	// modulesMu guards modules. It's a read-mostly map (modules are
	// added on LoadModule, removed on Module.Close) so a plain sync.Mutex
	// is fine — we don't need RWMutex churn for the few-per-second
	// transitions we expect.
	modulesMu sync.Mutex
	modules   map[string]*Module

	// closed is set non-zero after Close returns. Subsequent LoadModule
	// calls return ErrRuntimeClosed. Atomic so the check is lock-free
	// on the hot path.
	closed atomic.Bool

	// extraHosts is the list of host-module builders passed in via
	// WithHostModule. They are instantiated against this runtime
	// alongside the built-in "env" module. The capability ABI (#107)
	// uses this seam to register its own host functions without
	// modifying this package.
	extraHosts []HostModuleBuilder

	// enforcer applies the configured resource Limits to every call.
	// One enforcer per Runtime; constructed in New from the resolved
	// limits.Limits and shared across all Modules. Never nil — when
	// no WithLimits is supplied, New installs a defaults-backed
	// enforcer so Module.Call has a single uniform code path.
	enforcer *limits.Enforcer

	// logPublisher is the optional sink hostGnLog forwards to AFTER
	// the structured-logger write. nil when no publisher has been
	// installed; the host function nil-checks before each call to
	// keep the fast path branch-predictable.
	//
	// This is the seam that the dev CLI's log streaming uses (#271):
	// the debug.LogHub satisfies LogPublisher and is registered here
	// at construction by WithLogPublisher.
	logPublisher LogPublisher

	// platform carries the optional plugin-platform ABI configuration
	// (secrets, audit, cron). nil when WithPlatform was not supplied;
	// the host-function dispatchers nil-check before invoking the
	// underlying services. Configured via WithPlatform — see
	// host_platform.go.
	platform *platformContext
}

// wazeroRuntime is an alias for wazero.Runtime, kept under a local
// name so the public Option type doesn't force callers to import
// wazero just to read the signature. The underlying type is identical.
type wazeroRuntime = wazero.Runtime

// HostModuleBuilder is the seam future packages (capability ABI #107)
// use to register additional host modules into the Runtime.
//
// A HostModuleBuilder is a function that takes the wazero runtime and
// instantiates a host module against it (or returns an error). The
// Runtime calls each builder once during New().
//
// Implementers typically wrap wazero.HostModuleBuilder:
//
//	WithHostModule(func(ctx context.Context, rt wazero.Runtime) error {
//	    _, err := rt.NewHostModuleBuilder("gonext_caps").
//	        NewFunctionBuilder().WithFunc(...).Export("kv_read").
//	        Instantiate(ctx)
//	    return err
//	})
type HostModuleBuilder func(ctx context.Context, rt wazeroRuntime) error

// Option configures a Runtime at construction time.
type Option func(*runtimeConfig)

type runtimeConfig struct {
	logger           *slog.Logger
	timeSource       TimeSource
	memoryLimitPages uint32
	extraHosts       []HostModuleBuilder
	logPublisher     LogPublisher

	// limits, when non-nil, overrides the default resource envelope.
	// nil means "use limits.Default()". The separate flag (instead of
	// always-default) lets WithLimits and WithMemoryLimitPages
	// coexist: whichever option lands last wins for the memory cap
	// specifically.
	limits *limits.Limits

	// platform, when non-nil, installs the plugin platform ABIs
	// (gn_secrets_get and, via follow-up commits, gn_audit_emit and
	// gn_cron_register). Configured via WithPlatform — see
	// host_platform.go.
	platform *platformContext
}

// WithLogger injects the structured logger. If unset, slog.Default is
// used.
func WithLogger(l *slog.Logger) Option {
	return func(c *runtimeConfig) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithTimeSource replaces time.Now for the gn_time_ms host function.
// Tests pin this to a fixed instant. Production code leaves it unset.
func WithTimeSource(fn TimeSource) Option {
	return func(c *runtimeConfig) {
		if fn != nil {
			c.timeSource = fn
		}
	}
}

// WithMemoryLimitPages overrides the per-module memory cap in 64 KiB
// pages. The default is 256 (16 MiB). Values above wazero's hard cap
// (currently 65536 pages = 4 GiB) panic — that matches wazero's own
// validation.
//
// Plugins requesting more pages in their module declaration than this
// limit allows are rejected at instantiation time with a wazero error
// surfaced as a *CompileError.
func WithMemoryLimitPages(pages uint32) Option {
	return func(c *runtimeConfig) {
		if pages > 0 {
			c.memoryLimitPages = pages
		}
	}
}

// WithHostModule registers an additional host module builder. Multiple
// WithHostModule options compose; each builder runs once during New(),
// in order. A builder failure aborts New() and returns the error.
//
// This is the extension point the capability ABI (#107) plugs into.
func WithHostModule(b HostModuleBuilder) Option {
	return func(c *runtimeConfig) {
		if b != nil {
			c.extraHosts = append(c.extraHosts, b)
		}
	}
}

// WithLogPublisher installs a sink for gn_log calls. Each guest
// gn_log invocation will, in addition to the structured-logger write,
// invoke p.Publish with the module name, the raw int32 level, and the
// decoded message. Passing nil clears the publisher.
//
// The intended consumer is the debug package's LogHub, which fans the
// events out to subscribers (the dev CLI's `--logs` flag). Production
// hosts that don't want streaming leave this unset.
//
// Multiple WithLogPublisher calls keep only the last value — there is
// no chaining. Callers that want fan-out should compose their own
// publisher and pass that one.
func WithLogPublisher(p LogPublisher) Option {
	return func(c *runtimeConfig) {
		c.logPublisher = p
	}
}

// WithLimits installs a resource envelope for the runtime. The Limits
// govern CPU-time per call (soft + hard deadlines), memory pages, and
// the per-plugin instance cap consulted by the pool.
//
// Order matters: if both WithLimits and WithMemoryLimitPages are
// supplied, the *later* one wins for the memory cap. CPU and instance
// limits only come from WithLimits — there is no separate option.
//
// Invalid Limits (e.g., hard deadline < soft deadline) are reported
// by New() as an error rather than panicking at construction time,
// keeping the option API non-fallible.
func WithLimits(l limits.Limits) Option {
	return func(c *runtimeConfig) {
		copy := l
		c.limits = &copy
		// Propagate the memory cap so callers using only WithLimits
		// see the full Limits applied — including memory. Callers
		// that want a different memory page count layer
		// WithMemoryLimitPages *after* WithLimits.
		if copy.MemoryPages > 0 {
			c.memoryLimitPages = copy.MemoryPages
		}
	}
}

// New constructs a Runtime. The provided context is used only for the
// initial wazero runtime + host-module instantiation; it is NOT stored
// for later use.
//
// The Runtime must be Close()d when the host process is shutting down.
// Closing the Runtime closes every Module it owns.
func New(ctx context.Context, opts ...Option) (*Runtime, error) {
	cfg := runtimeConfig{
		logger:           slog.Default(),
		timeSource:       time.Now,
		memoryLimitPages: defaultMemoryLimitPages,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Resolve the effective Limits. WithLimits supplies one;
	// otherwise we synthesise one from defaults plus whatever the
	// scalar WithMemoryLimitPages option set. The result MUST validate
	// — if it doesn't, we fail New rather than risk runtime surprises.
	effectiveLimits := limits.Default()
	if cfg.limits != nil {
		effectiveLimits = *cfg.limits
	}
	// Always sync the memory cap back to the limits struct: the
	// runtime config's scalar field is what the rest of the codepath
	// has historically used, so it remains the source of truth for
	// wazero-level memory enforcement.
	effectiveLimits.MemoryPages = cfg.memoryLimitPages
	enforcer, err := limits.NewEnforcer(effectiveLimits)
	if err != nil {
		return nil, fmt.Errorf("runtime: New: invalid limits: %w", err)
	}

	wazeroCfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(cfg.memoryLimitPages).
		// WithCloseOnContextDone makes ctx cancellation propagate into
		// running guest functions as a trap, so a runaway plugin can be
		// killed by the caller's ctx timeout. This is the foundation of
		// the per-call deadline policy (#15 builds on it).
		WithCloseOnContextDone(true)

	wRT := wazero.NewRuntimeWithConfig(ctx, wazeroCfg)

	rt := &Runtime{
		wazeroRT:     wRT,
		logger:       cfg.logger,
		timeSource:   cfg.timeSource,
		modules:      make(map[string]*Module),
		extraHosts:   cfg.extraHosts,
		enforcer:     enforcer,
		logPublisher: cfg.logPublisher,
		platform:     cfg.platform,
	}

	// Register the built-in "env" host module that exposes the minimum
	// host ABI (gn_log, gn_panic, gn_time_ms). If this fails, we have a
	// fundamentally broken wazero — surface and abort.
	if err := rt.registerEnvHost(ctx); err != nil {
		// Best-effort close so we don't leak the runtime.
		_ = wRT.Close(ctx)
		return nil, fmt.Errorf("runtime: New: register env host: %w", err)
	}

	// Run any caller-supplied host module builders. Order is preserved.
	for i, b := range rt.extraHosts {
		if err := b(ctx, wRT); err != nil {
			_ = wRT.Close(ctx)
			return nil, fmt.Errorf("runtime: New: extra host #%d: %w", i, err)
		}
	}

	return rt, nil
}

// LoadModule compiles the supplied .wasm bytes, instantiates them as a
// module named `name`, and returns a Module handle.
//
// `name` must be unique across the lifetime of the Runtime. If a module
// with the same name is already loaded, LoadModule returns an error
// rather than silently replacing — duplicate names would make
// host-side bookkeeping ambiguous. Callers that want to re-load (e.g.
// after Module.Close) can do so once the prior name is no longer in
// use.
//
// On success the returned Module owns its wazero handles and is safe
// to use from any goroutine (Call serializes internally; see
// module.go).
//
// On failure of compilation, returns *CompileError. On other failures
// (duplicate name, runtime closed, instantiation error) returns a
// plain wrapped error.
func (r *Runtime) LoadModule(ctx context.Context, name string, wasmBytes []byte) (*Module, error) {
	if r.closed.Load() {
		return nil, ErrRuntimeClosed
	}
	if name == "" {
		return nil, fmt.Errorf("runtime: LoadModule: name is required")
	}
	if len(wasmBytes) == 0 {
		return nil, fmt.Errorf("runtime: LoadModule: wasmBytes is empty")
	}

	// Compile first — failures here are pure module errors and don't
	// touch the modules map. CompileModule does NOT panic on malformed
	// input; wazero's decoder is robust against the kind of byte-soup a
	// malicious bundle could ship.
	compiled, err := r.wazeroRT.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, &CompileError{Module: name, Cause: err}
	}

	// Take the slot before instantiation so two concurrent callers
	// can't both win the "module named X" race. If we later fail to
	// instantiate, the deferred cleanup releases the slot.
	r.modulesMu.Lock()
	if _, exists := r.modules[name]; exists {
		r.modulesMu.Unlock()
		_ = compiled.Close(ctx)
		return nil, fmt.Errorf("runtime: LoadModule: module %q already loaded", name)
	}

	// Placeholder so the slot is reserved. We replace with the real
	// Module pointer after instantiation succeeds.
	r.modules[name] = nil
	r.modulesMu.Unlock()

	moduleCfg := wazero.NewModuleConfig().
		WithName(name).
		// Do NOT inherit stdio — plugins must use gn_log for output.
		// This is a deliberate sandboxing choice; surfacing stdout via
		// the host would let plugins dump arbitrary bytes into the
		// runtime's stdout stream, bypassing the structured-logging
		// pipeline.
		WithStartFunctions() // disable WASI _start auto-invoke

	inst, err := r.wazeroRT.InstantiateModule(ctx, compiled, moduleCfg)
	if err != nil {
		r.modulesMu.Lock()
		delete(r.modules, name)
		r.modulesMu.Unlock()
		_ = compiled.Close(ctx)
		return nil, fmt.Errorf("runtime: LoadModule: instantiate %q: %w", name, err)
	}

	m := &Module{
		name:     name,
		instance: inst,
		compiled: compiled,
		runtime:  r,
	}

	r.modulesMu.Lock()
	r.modules[name] = m
	r.modulesMu.Unlock()

	return m, nil
}

// Close shuts down the runtime, closing every Module it owns and
// releasing wazero's compiled-module cache.
//
// Close is idempotent — repeat calls return nil. After Close, all
// LoadModule calls return ErrRuntimeClosed.
//
// The ctx is passed to wazero.Runtime.Close; canceling it does not
// prevent close from succeeding.
func (r *Runtime) Close(ctx context.Context) error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Drain the modules map BEFORE closing the wazero runtime. wazero
	// will close every module it owns on rRT.Close, but our Module
	// wrappers need to be marked closed too so a stale handle doesn't
	// keep dispatching calls into a dead instance.
	r.modulesMu.Lock()
	mods := make([]*Module, 0, len(r.modules))
	for _, m := range r.modules {
		if m != nil {
			mods = append(mods, m)
		}
	}
	r.modules = nil
	r.modulesMu.Unlock()

	for _, m := range mods {
		// Mark each Module closed; the underlying wazero close happens
		// via the runtime-level Close below.
		m.markClosed()
	}

	return r.wazeroRT.Close(ctx)
}

// IsClosed reports whether Close has been called. Mostly useful in
// tests and admin probes.
func (r *Runtime) IsClosed() bool { return r.closed.Load() }

// Enforcer exposes the runtime's resource enforcer. It is never nil —
// even a runtime constructed without WithLimits has a defaults-backed
// enforcer.
//
// The pool (#9) calls Enforcer().Acquire(name) before checking out a
// new instance. Test code uses it to inspect counters.
func (r *Runtime) Enforcer() *limits.Enforcer { return r.enforcer }

// removeModule is called by Module.Close to drop the slot. Safe to
// call after Runtime.Close — the map is nil, the delete is a no-op.
func (r *Runtime) removeModule(name string) {
	r.modulesMu.Lock()
	defer r.modulesMu.Unlock()
	if r.modules != nil {
		delete(r.modules, name)
	}
}
