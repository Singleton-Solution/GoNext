package wpcompat

import (
	"context"
	"errors"
	"sync"

	"github.com/Singleton-Solution/GoNext/packages/go/hooks"
)

// ErrUnknownAlias is returned by Subscribe when the supplied WP name is
// not in the Aliases table. Catching typos at registration time is the
// whole point of returning this rather than silently accepting any name
// the caller asks for — a plugin that subscribes to "the_contetn" should
// fail loudly, not silently never fire.
var ErrUnknownAlias = errors.New("wpcompat: unknown WP hook alias")

// forwardPriority is the priority the bridge installs its forwarders at.
//
// We pick a high (=late) value so wpcompat forwarders run AFTER core's
// own filter chain has had its turn. The intent: when a WP-side plugin
// subscribes to the_content, they want to see the final, post-core-
// processing string the page renderer would actually emit — not the raw
// pre-core string. If forwarders ran early, the WP-side handler would
// receive an intermediate value and would have to know which other core
// filters had run, which is exactly the WP "everyone-filters-on-arrival"
// fragility the platform is trying to avoid.
//
// The number 1000 is a round number well clear of the default WP "10"
// most plugins use. It is documented in docs/02-plugin-system.md so
// plugin authors can reason about where the WP forwarder sits.
const forwardPriority = 1000

// Bridge owns the set of forwarding registrations installed on a Bus.
//
// The lifecycle is one-shot: Register installs forwarders, Close removes
// them. Re-registering on the same Bus first closes the prior set so
// there is no possibility of double-forwarding. A Bridge is safe for
// concurrent use after Register has returned; concurrent calls to
// Register are serialized via mu.
//
// Bridge owns no global state; you can have two Bridges (e.g. one per
// test) on two Buses simultaneously without interference.
type Bridge struct {
	mu sync.Mutex
	// offs holds the unsubscribe closures returned by Bus.Register* for
	// every forwarder we installed, so Close can tear them all down.
	// Nil entries are not used — we treat the slice as append-only and
	// reset it in Register.
	offs []func()
	// bus is the Bus we installed against. Stored so Close can refuse to
	// run twice and so future helpers (e.g. ResubscribeOnReload) have
	// the reference handy. Nil before Register.
	bus *hooks.Bus
}

// NewBridge returns a fresh Bridge with no forwarders installed.
// Call Register(bus) to wire it up.
func NewBridge() *Bridge {
	return &Bridge{}
}

// Register installs a forwarding registration on bus for every entry in
// the Aliases table. After Register returns, an emission on a native
// hook (e.g. ApplyFilters("core.filter.the_content", ...)) will also
// drive the chain on the WP-name (the_content), and vice versa via
// Subscribe.
//
// Calling Register twice is safe: the second call first invokes Close
// on the existing forwarders, then installs the fresh set. This is the
// pattern for "plugin host reload" — the host can rebuild its bridge
// without leaving zombie forwarders on the bus.
//
// Returns an error only if bus is nil; everything else is infallible
// (registration on the Bus itself does not fail).
func (b *Bridge) Register(bus *hooks.Bus) error {
	if bus == nil {
		return errors.New("wpcompat: Bridge.Register: bus is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	// Drop the previous registration set if any — this makes Register
	// idempotent and re-callable on reload.
	for _, off := range b.offs {
		off()
	}
	b.offs = b.offs[:0]
	b.bus = bus

	for wpName, alias := range Aliases {
		wpName, alias := wpName, alias // capture for the closure below
		switch alias.Direction {
		case Filter:
			off := bus.RegisterFilter(alias.NativeName, forwardPriority,
				func(ctx context.Context, value any, args ...any) (any, error) {
					// Forward into the WP-name chain. Whatever that
					// chain produces becomes the next value flowing
					// through the native chain — this is the
					// bidirectional bit: WP-side plugins can modify a
					// value and have native consumers see it.
					adapted := value
					if alias.PayloadAdapter != nil {
						adapted = alias.PayloadAdapter(value)
					}
					out, err := bus.ApplyFilters(ctx, wpName, adapted, args...)
					if err != nil {
						return value, err
					}
					return out, nil
				})
			b.offs = append(b.offs, off)

		case Action:
			off := bus.RegisterAction(alias.NativeName, forwardPriority,
				func(ctx context.Context, args ...any) error {
					// Adapter, when present, receives the raw args
					// slice and produces either a single replacement
					// payload (one WP-shaped struct) or an unchanged
					// slice. The Bus call below splats either back
					// into args... in the WP-name chain.
					if alias.PayloadAdapter != nil {
						adapted := alias.PayloadAdapter(any(args))
						switch v := adapted.(type) {
						case []any:
							return bus.Do(ctx, wpName, v...)
						default:
							return bus.Do(ctx, wpName, v)
						}
					}
					return bus.Do(ctx, wpName, args...)
				})
			b.offs = append(b.offs, off)
		}
	}
	return nil
}

// Close uninstalls every forwarder this Bridge had registered. After
// Close, the Bus no longer cross-emits to WP names — though any handlers
// the caller registered on those names directly remain in place (Close
// is intentionally surgical; it does not own subscriber registrations).
//
// Close is safe to call multiple times; the second and subsequent calls
// are no-ops. It is safe to call from any goroutine.
func (b *Bridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, off := range b.offs {
		off()
	}
	b.offs = nil
	b.bus = nil
}
