package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	asynqlib "github.com/hibiken/asynq"

	pluginasynq "github.com/Singleton-Solution/GoNext/packages/go/jobs/asynq"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/capabilities"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
)

// DefaultMaxRetry is the retry budget the bridge installs on every
// auto-generated TaskSpec for a plugin job. We pick 5 to match the
// platform's general "transient failure" tolerance — outbound HTTP
// providers, third-party APIs, and ratelimits typically recover within
// the asynq exponential-backoff window of 5 attempts (~30s total wall
// clock). Plugins that need a different policy should be able to
// override per-job once the manifest grows a richer jobs[] schema
// (issue #268 follow-up); the bridge today applies the same default to
// every plugin-declared job for simplicity.
const DefaultMaxRetry = 5

// DefaultTimeout is the per-invocation timeout. Plugin jobs run inside
// the WASM sandbox with its own CPU-time limits enforced by the
// runtime Enforcer, but asynq's own timeout exists as a backstop
// against handler-level hangs (e.g., a host-side I/O outside the
// sandbox). 30s matches the platform's default for batch tasks; it's
// long enough for most realistic plugin work and short enough that a
// hung handler doesn't block a worker slot for hours.
const DefaultTimeout = 30 * time.Second

// Bridge connects a plugin module to the host's TaskSpec registry +
// Asynq dispatch chassis.
//
// Construction: NewBridge(plugin, dispatcher, registry, checker). The
// Bridge holds onto the names of every TaskSpec it registered so a
// subsequent Unregister can mark them for removal — though the current
// taskspec.Registry is append-only (the registry has no delete API by
// design; first-writer-wins is the documented semantics). Unregister
// therefore only marks the bridge closed; the TaskSpec.Handler closure
// detects the closed flag at invocation time and returns an error so
// asynq stops dispatching to a dead bridge.
//
// This is the same hot-reload behavior the hooks bridge has: Unregister
// is idempotent and tear-down is atomic from the asynq worker's
// perspective. On reload, the lifecycle Manager constructs a fresh
// Bridge (and a fresh dispatcher around a fresh Module) and calls
// Register again — the new TaskSpec carries a new closure pointing at
// the new dispatcher.
//
// Bridge is goroutine-safe: Register/Unregister are serialized through
// a mutex. Job handler invocations (the closures the Bridge installs
// on the registry) are NOT serialized by Bridge itself — the dispatcher
// serializes calls into the guest through the underlying Module.
type Bridge struct {
	plugin     string // plugin slug, used in error messages and log lines
	dispatcher *Dispatcher
	registry   *taskspec.Registry
	checker    *capabilities.Checker
	logger     *slog.Logger
	queue      string
	maxRetry   int
	timeout    time.Duration

	mu          sync.Mutex
	registered  []string // names of TaskSpecs the bridge installed (for diagnostics)
	closed      bool
}

// Option configures a Bridge at construction time.
type Option func(*Bridge)

// WithLogger sets the slog.Logger used by the bridge for diagnostic
// output. Defaults to slog.Default. The bridge logs proxy-call failures
// at WARN — proxy errors travel through the asynq error path, so this
// log is supplementary, not authoritative.
func WithLogger(l *slog.Logger) Option {
	return func(b *Bridge) {
		if l != nil {
			b.logger = l
		}
	}
}

// WithQueue overrides the asynq queue every plugin-declared TaskSpec
// lands on. Defaults to pluginasynq.QueuePlugin ("plugin"). Operators
// that want to shard plugins across queues (a "trusted-plugins"
// queue, say) can override per-bridge.
func WithQueue(q string) Option {
	return func(b *Bridge) {
		if q != "" {
			b.queue = q
		}
	}
}

// WithMaxRetry overrides the default retry budget for every job the
// bridge registers. Negative values are clamped to 0 by asynq (no
// retry); we don't second-guess that here.
func WithMaxRetry(n int) Option {
	return func(b *Bridge) {
		b.maxRetry = n
	}
}

// WithTimeout overrides the default per-invocation timeout. Values ≤ 0
// disable the timeout (matching asynq's "zero means no deadline"
// contract). Plugins that need a longer timeout — e.g., a long-running
// ETL job — override it here at the bridge layer.
func WithTimeout(t time.Duration) Option {
	return func(b *Bridge) {
		b.timeout = t
	}
}

// NewBridge constructs a Bridge.
//
// pluginSlug is informational — it tags log lines and JobError fields
// so failures attribute to the right plugin in a multi-plugin host.
//
// dispatcher MUST wrap an already-loaded Module. The bridge does not
// validate the module exports until Register is called (the deferred
// resolution mirrors Dispatcher's own laziness).
//
// registry is the TaskSpec registry the bridge will install proxy
// TaskSpecs into. Pass the same registry the rest of the host uses
// (taskspec.Default in production); tests construct their own.
//
// checker enforces the jobs.enqueue capability at Register time. nil
// is permitted for tests that don't want to wire a Checker, but
// production wiring always passes one — without it, ANY plugin can
// declare jobs in its manifest and bypass the cap grant flow. This is
// the load-bearing security check for the bridge.
func NewBridge(pluginSlug string, dispatcher *Dispatcher, registry *taskspec.Registry, checker *capabilities.Checker, opts ...Option) (*Bridge, error) {
	if dispatcher == nil {
		return nil, errors.New("abi/jobs: dispatcher is nil")
	}
	if registry == nil {
		return nil, errors.New("abi/jobs: registry is nil")
	}
	b := &Bridge{
		plugin:     pluginSlug,
		dispatcher: dispatcher,
		registry:   registry,
		checker:    checker,
		logger:     slog.Default(),
		queue:      pluginasynq.QueuePlugin,
		maxRetry:   DefaultMaxRetry,
		timeout:    DefaultTimeout,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b, nil
}

// Register reads the manifest's jobs declarations and installs a
// TaskSpec for each on the registry.
//
// Behavior:
//
//   - Each name in manifest.Jobs becomes a taskspec.TaskSpec whose
//     Handler proxies into Dispatcher.InvokeJob. The Handler envelopes
//     the asynq Task ID (idempotency key), retry count, and payload
//     bytes before reaching the guest.
//
//   - The TaskSpec.Queue is the bridge's configured queue (defaults to
//     pluginasynq.QueuePlugin).
//
//   - MaxRetry and Timeout come from the bridge's configured defaults.
//     Per-job overrides will land when the manifest grows a richer
//     jobs[] schema.
//
// Capability check: if the bridge was constructed with a Checker (the
// production wiring always passes one), Register requires the plugin
// to hold the jobs.enqueue capability. Manifests declaring jobs[]
// without the matching cap are rejected with ErrCapabilityDenied; no
// TaskSpecs are registered. This mirrors the manifest-install gate:
// a plugin cannot subscribe to jobs it lacks the cap to schedule.
//
// Returns the number of TaskSpecs installed and any error. On error,
// the partially-installed TaskSpecs are LEFT in the registry — the
// taskspec.Registry has no delete API, so rollback is not possible
// at this layer. The lifecycle Manager handles activation-failure
// rollback at a higher layer (it closes the Module, which Unregister
// then detects).
func (b *Bridge) Register(ctx context.Context, m *manifest.Manifest) (int, error) {
	if m == nil {
		return 0, errors.New("abi/jobs: manifest is nil")
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, errors.New("abi/jobs: bridge is closed")
	}
	b.mu.Unlock()

	if len(m.Jobs) == 0 {
		// Nothing to register. Not an error — many plugins declare no
		// jobs (capability-only plugins, hook-only plugins).
		return 0, nil
	}

	// Capability gate. A plugin declaring jobs without the cap is
	// rejected BEFORE any TaskSpec is installed; the operator should
	// see a single clear failure rather than half-wired state.
	//
	// We skip the check only when no Checker was supplied (test path).
	// Production wiring always supplies one.
	//
	// The error wrapping joins our bridge-local sentinel
	// (ErrCapabilityDenied) with the underlying capabilities
	// denial via errors.Join so callers can errors.Is either one —
	// the platform layer wants to react to the capability sentinel,
	// the abi/jobs caller wants to react to the bridge sentinel.
	if b.checker != nil {
		if err := b.checker.MustAllow(ctx, CapabilityID); err != nil {
			return 0, fmt.Errorf("%w: %w", ErrCapabilityDenied, err)
		}
	}

	installed := 0
	for _, name := range m.Jobs {
		// Capture the loop variable for the closure. Without this each
		// installed TaskSpec.Handler would see the same final loop
		// value and route every job to the last name in m.Jobs.
		jobName := name
		spec := taskspec.TaskSpec{
			Name:     jobName,
			Queue:    b.queue,
			MaxRetry: b.maxRetry,
			Timeout:  b.timeout,
			Handler:  b.handler(jobName),
		}
		if err := b.registry.Register(spec); err != nil {
			// First-writer-wins in the registry — two plugins declaring
			// the same job name produces an error here. We surface it
			// rather than overwriting silently. The error wraps the
			// registry's ErrAlreadyRegistered.
			return installed, fmt.Errorf("abi/jobs: register %q: %w", jobName, err)
		}
		b.mu.Lock()
		b.registered = append(b.registered, jobName)
		b.mu.Unlock()
		installed++
	}

	return installed, nil
}

// Unregister marks the bridge closed so subsequent invocations through
// the registered TaskSpec handlers short-circuit with an error. The
// underlying TaskSpec entries in the registry remain — the registry's
// first-writer-wins contract means an Unregister + re-Register cycle
// for the same name on the same Registry would fail at Register time.
//
// Hot-reload contract: when a plugin module is reloaded, the lifecycle
// Manager constructs a FRESH registry-or-overlay for the new module,
// not the same one. The bridge's job is to ensure the old closure
// stops reaching the old (closed) Module, which Unregister achieves by
// flipping the closed flag — the new Module gets its own bridge with
// its own registered closures.
//
// Idempotent — calling Unregister twice is a no-op.
//
// Unregister does NOT close the underlying Dispatcher or Module —
// lifetime of the plugin module is owned by the lifecycle Manager,
// which holds the Module handle and decides when to close it.
func (b *Bridge) Unregister() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
}

// Registered returns the names of the TaskSpecs the bridge installed,
// in registration order. Useful for diagnostics and for tests that
// want to assert "this bridge owns these job names".
//
// Safe for concurrent use.
func (b *Bridge) Registered() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.registered))
	copy(out, b.registered)
	return out
}

// handler returns the closure installed as TaskSpec.Handler for a
// specific job name. The closure:
//
//  1. Refuses to run if the bridge is closed (post-Unregister).
//  2. Extracts the asynq Task ID from the context for idempotency.
//  3. Extracts the retry count for the envelope.
//  4. Marshals the envelope.
//  5. Delegates to Dispatcher.InvokeJob.
//
// The closure deliberately does NOT wrap errors with asynq.SkipRetry:
// a guest-reported error or trap should retry per the spec's policy.
// Operators that need to skip retries on specific status values can
// add that mapping at the lifecycle Manager layer once the manifest
// jobs[] schema grows a per-job retry hint.
func (b *Bridge) handler(jobName string) func(ctx context.Context, payload []byte) error {
	return func(ctx context.Context, payload []byte) error {
		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			// The bridge has been torn down. Returning an error here
			// makes asynq retry — which is fine: the operator just
			// activated a new bridge for this plugin (hot reload),
			// the next pickup hits a registry that points at the new
			// bridge. We do NOT wrap with asynq.SkipRetry because the
			// caller WANTS the retry.
			return fmt.Errorf("abi/jobs: bridge closed for plugin %q job %q", b.plugin, jobName)
		}

		// Idempotency key = asynq Task ID. asynq.GetTaskID returns
		// ok=false when called outside an asynq dispatch context (e.g.
		// from a direct test invocation of the registered Handler);
		// the envelope tolerates an empty key.
		taskID, _ := asynqlib.GetTaskID(ctx)
		retryCount, _ := asynqlib.GetRetryCount(ctx)

		envelope, err := MarshalJobEnvelope(taskID, retryCount, payload)
		if err != nil {
			b.logger.WarnContext(ctx, "abi/jobs: envelope marshal failed",
				slog.String("plugin", b.plugin),
				slog.String("job", jobName),
				slog.Any("err", err),
			)
			return err
		}

		if err := b.dispatcher.InvokeJob(ctx, jobName, envelope); err != nil {
			b.logger.WarnContext(ctx, "abi/jobs: invoke failed",
				slog.String("plugin", b.plugin),
				slog.String("job", jobName),
				slog.String("task_id", taskID),
				slog.Int("retry_count", retryCount),
				slog.Any("err", err),
			)
			return err
		}
		return nil
	}
}
