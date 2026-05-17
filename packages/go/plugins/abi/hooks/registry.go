package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	hostbus "github.com/Singleton-Solution/GoNext/packages/go/hooks"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/manifest"
)

// DefaultPriority is the priority handed to host-bus registrations
// proxying through to a plugin. It matches WordPress's default (10) so
// plugin authors who think in WP priorities get the same ordering they
// would on that platform.
//
// Operators that want to override per-hook can pass WithPriorityFunc to
// Register; the manifest may eventually grow a priority hint per-hook
// (issue #96 candidate) at which point the bridge will read it from
// there.
const DefaultPriority = 10

// Bridge connects a plugin module to the host's hook Bus.
//
// Construction: NewBridge(plugin, dispatcher, bus). The Bridge holds
// onto unsubscribe closures for every registration it makes so a
// subsequent Unregister tears everything down atomically — which the
// lifecycle Manager calls on deactivation.
//
// Bridge is goroutine-safe: Register/Unregister are serialized through
// a mutex. Hook handler invocations (the closures the Bridge installs
// on the bus) are NOT serialized by Bridge itself — the bus already
// guarantees one invocation per handler at a time per chain position,
// and the Dispatcher serializes calls into the guest through the
// underlying Module.
type Bridge struct {
	plugin     string // plugin slug, used in error messages and log lines
	dispatcher *Dispatcher
	bus        *hostbus.Bus
	logger     *slog.Logger
	priority   PriorityFunc

	mu     sync.Mutex
	unsubs []func()
	closed bool
}

// HookKind discriminates action hooks from filter hooks for the
// PriorityFunc callback. We define our own type rather than re-exporting
// from the bus package because the bus's internal kind constants are
// unexported — and the bridge only needs the two-way distinction.
type HookKind uint8

const (
	// KindAction selects an action hook (fire-and-forget, aggregated
	// errors).
	KindAction HookKind = iota
	// KindFilter selects a filter hook (value-transforming chain).
	KindFilter
)

// String returns "action" or "filter" for the kind. Mirrors the label
// strings the bus uses in its metrics.
func (k HookKind) String() string {
	switch k {
	case KindAction:
		return "action"
	case KindFilter:
		return "filter"
	default:
		return "unknown"
	}
}

// PriorityFunc returns the priority a given hook should register at on
// the host bus. The bridge calls it once per hook at Register time.
// Returning DefaultPriority for unrecognized hooks is the safe default.
type PriorityFunc func(hookName string, kind HookKind) int

// Option configures a Bridge at construction time.
type Option func(*Bridge)

// WithLogger sets the slog.Logger used by the bridge for diagnostic
// output. Defaults to slog.Default. The bridge logs proxy-call failures
// at WARN — proxy errors travel through the bus's standard error
// channel, so this log is supplementary, not authoritative.
func WithLogger(l *slog.Logger) Option {
	return func(b *Bridge) {
		if l != nil {
			b.logger = l
		}
	}
}

// WithPriorityFunc sets a function that returns the priority for each
// hook. Defaults to "always DefaultPriority". The future manifest
// extension (issue #96 candidate) will provide per-hook priority hints;
// until then operators that need ordering control set it here.
func WithPriorityFunc(fn PriorityFunc) Option {
	return func(b *Bridge) {
		if fn != nil {
			b.priority = fn
		}
	}
}

// NewBridge constructs a Bridge.
//
// pluginSlug is informational — it tags log lines and HookError fields
// so failures attribute to the right plugin in a multi-plugin host.
//
// dispatcher MUST wrap an already-loaded Module. The bridge does not
// validate the module exports until Register is called (the deferred
// resolution mirrors Dispatcher's own laziness).
//
// bus is the host-side hook bus the bridge will register proxy
// callbacks on. Pass the same Bus the rest of the host uses; if
// the plugin shares the bus with other plugins, ordering between them
// follows the bus's priority semantics.
func NewBridge(pluginSlug string, dispatcher *Dispatcher, bus *hostbus.Bus, opts ...Option) (*Bridge, error) {
	if dispatcher == nil {
		return nil, errors.New("abi/hooks: dispatcher is nil")
	}
	if bus == nil {
		return nil, errors.New("abi/hooks: bus is nil")
	}
	b := &Bridge{
		plugin:     pluginSlug,
		dispatcher: dispatcher,
		bus:        bus,
		logger:     slog.Default(),
		priority:   func(string, HookKind) int { return DefaultPriority },
	}
	for _, opt := range opts {
		opt(b)
	}
	return b, nil
}

// Register reads the manifest's hooks declarations and installs a
// proxy callback on the host bus for each.
//
// Behavior:
//
//   - Each action in manifest.Hooks.Actions becomes a synchronous
//     action handler on the bus that calls Dispatcher.InvokeAction.
//
//   - Each filter in manifest.Hooks.Filters becomes a filter handler
//     that calls Dispatcher.InvokeFilter. The filter handler converts
//     the bus's `any` value to JSON bytes via encoding/json, sends it
//     to the guest, and converts the returned JSON bytes back to an
//     `any` via json.Unmarshal into json.RawMessage (so downstream
//     filter handlers receive the raw bytes — they decode if they
//     care).
//
// Register is idempotent in the sense that calling it twice on the
// same Bridge installs the callbacks twice (each manifest pass adds
// new registrations); the manifest contract says it's called once per
// activation, so the second call would be a bug. Bridge does not
// guard against it — the bus tolerates duplicates fine.
//
// Returns the number of registrations installed and any error. On
// error, the partially-installed registrations are left in place so
// the caller can Unregister to roll back; this mirrors what the
// lifecycle Manager already expects on activation failure.
func (b *Bridge) Register(ctx context.Context, m *manifest.Manifest) (int, error) {
	if m == nil {
		return 0, errors.New("abi/hooks: manifest is nil")
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, errors.New("abi/hooks: bridge is closed")
	}
	b.mu.Unlock()

	if m.Hooks == nil {
		// Nothing to register. Not an error — many plugins declare no
		// hooks (capability-only plugins, jobs-only plugins).
		return 0, nil
	}

	installed := 0
	for _, name := range m.Hooks.Actions {
		hookName := name
		prio := b.priority(hookName, KindAction)
		unsub := b.bus.RegisterAction(hookName, prio,
			func(ctx context.Context, args ...any) error {
				return b.proxyAction(ctx, hookName, args)
			})
		b.mu.Lock()
		b.unsubs = append(b.unsubs, unsub)
		b.mu.Unlock()
		installed++
	}

	for _, name := range m.Hooks.Filters {
		hookName := name
		prio := b.priority(hookName, KindFilter)
		unsub := b.bus.RegisterFilter(hookName, prio,
			func(ctx context.Context, value any, args ...any) (any, error) {
				return b.proxyFilter(ctx, hookName, value, args)
			})
		b.mu.Lock()
		b.unsubs = append(b.unsubs, unsub)
		b.mu.Unlock()
		installed++
	}

	return installed, nil
}

// Unregister tears down every host-bus registration the bridge
// installed and marks the bridge closed. Subsequent Register calls
// return an error. Idempotent — calling Unregister twice is a no-op.
//
// Unregister does NOT close the underlying Dispatcher or Module —
// lifetime of the plugin module is owned by the lifecycle Manager,
// which holds the Module handle and decides when to close it.
func (b *Bridge) Unregister() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	unsubs := b.unsubs
	b.unsubs = nil
	b.mu.Unlock()

	for _, fn := range unsubs {
		fn()
	}
}

// proxyAction is the closure shape installed for every action hook.
// It marshals the bus's variadic args into the ABI envelope and
// delegates to Dispatcher.InvokeAction.
//
// The action proxy returns the error the dispatcher produced; the bus
// aggregates errors from multiple handlers so a single plugin failure
// surfaces alongside other handlers' outcomes.
func (b *Bridge) proxyAction(ctx context.Context, hookName string, args []any) error {
	err := b.dispatcher.InvokeAction(ctx, hookName, args...)
	if err != nil {
		b.logger.WarnContext(ctx, "abi/hooks: action proxy failed",
			slog.String("plugin", b.plugin),
			slog.String("hook", hookName),
			slog.Any("err", err),
		)
	}
	return err
}

// proxyFilter is the closure shape installed for every filter hook.
// It converts the bus's untyped value to JSON, hands it to the guest,
// and converts the guest's reply back to a value the bus can carry.
//
// On guest failure the proxy returns the input value unchanged
// alongside the error, matching the bus's "last accepted value on
// non-short-circuit error" contract. The bus then halts the chain
// with that value and the error.
//
// The returned value is a json.RawMessage; downstream filter handlers
// that need typed access are responsible for json.Unmarshal — the same
// contract as if a Go-side handler had returned bytes.
func (b *Bridge) proxyFilter(ctx context.Context, hookName string, value any, args []any) (any, error) {
	// Convert the bus's any value into JSON bytes. We accept three
	// shapes pre-encoded by smart callers:
	//
	//   - json.RawMessage: pass through unchanged (skip re-encode)
	//   - []byte that decodes as JSON: pass through (we don't verify
	//     it parses; if the guest's decoder chokes, that's a bad
	//     payload status)
	//   - anything else: encoding/json.Marshal
	var raw json.RawMessage
	switch v := value.(type) {
	case nil:
		raw = json.RawMessage("null")
	case json.RawMessage:
		raw = v
	case []byte:
		// We can't tell if these bytes are JSON or not without a
		// validate-pass. The simpler contract is to treat []byte as
		// "already JSON" so calls that came from elsewhere on the bus
		// (where filter values are often pre-serialized post bodies)
		// don't get re-quoted as a JSON string.
		raw = json.RawMessage(v)
	default:
		buf, err := json.Marshal(value)
		if err != nil {
			b.logger.WarnContext(ctx, "abi/hooks: filter value marshal failed",
				slog.String("plugin", b.plugin),
				slog.String("hook", hookName),
				slog.Any("err", err),
			)
			return value, fmt.Errorf("abi/hooks: marshal filter value: %w", err)
		}
		raw = buf
	}

	result, err := b.dispatcher.InvokeFilter(ctx, hookName, raw, args...)
	if err != nil {
		b.logger.WarnContext(ctx, "abi/hooks: filter proxy failed",
			slog.String("plugin", b.plugin),
			slog.String("hook", hookName),
			slog.Any("err", err),
		)
		return value, err
	}
	return result, nil
}
