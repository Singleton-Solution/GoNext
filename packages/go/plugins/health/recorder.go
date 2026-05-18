package health

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/metrics"
)

// DefaultRingSize is the per-plugin trap-ring capacity. 100 entries is
// enough to span a few minutes of pathological trapping without
// growing without bound; an operator looking at the "what just broke"
// view rarely cares past the most-recent dozen. The default is
// surfaced as a const so tests can override via NewRecorderWithRing
// without re-declaring the magic number.
const DefaultRingSize = 100

// TrapEvent is one entry in the per-plugin ring buffer. It captures
// just enough to (a) render a useful incident card in the admin UI
// and (b) drive a `gonext plugin replay` invocation: the hook name,
// the payload bytes (if available), and the human-readable reason.
//
// ID is a process-stable identifier (monotonically increasing) the
// replay CLI passes back to the host to look up which event to
// re-run. The ID is not durable — a restart resets the counter and
// the ring — but the admin UI shows them only for the running
// process, so that matches the lifecycle.
type TrapEvent struct {
	// ID is the stable handle for this event within the running
	// process. The replay CLI passes this back to the server.
	ID uint64 `json:"id"`

	// Plugin is the plugin slug the event belongs to. The ring
	// buffer is already indexed by plugin so this is redundant for
	// internal use, but the JSON consumer (admin UI) needs it.
	Plugin string `json:"plugin"`

	// Hook is the hook name that was being dispatched when the
	// trap fired. Empty for non-hook traps (e.g. activation
	// failures).
	Hook string `json:"hook"`

	// Reason is the unredacted trap reason from the runtime
	// (wazero error string, gn_panic message, etc.).
	Reason string `json:"reason"`

	// NormalisedReason is the bounded-cardinality token used as
	// the Prometheus label. Useful in the UI as a stable grouping
	// key; the full Reason is the operator-facing detail.
	NormalisedReason string `json:"normalised_reason"`

	// Payload is the JSON payload the host marshalled for the
	// dispatched hook, if the producer captured it. nil means the
	// producer chose not to capture (e.g. payload exceeded the
	// inline cap). The replay CLI requires a non-nil payload to
	// re-run; an empty event surfaces as "replay unavailable".
	Payload []byte `json:"payload,omitempty"`

	// At is the wall-clock time the trap was observed.
	At time.Time `json:"at"`
}

// TrapDetail is the per-event metadata producers pass to
// Recorder.ObserveTrap. The Recorder fills in the ID and At fields
// itself; producers only supply the parts they know.
type TrapDetail struct {
	Hook    string
	Reason  string
	Payload []byte
}

// recorder is the concrete *Recorder implementation. It owns both the
// Prometheus collector set and the per-plugin trap ring. The split
// between the Recorder interface (in metrics.go) and this struct lets
// producers depend on the narrow observer surface while only the host
// wiring touches the registry.
type recorder struct {
	metrics  *metricsSet
	ringSize int

	// nextID is the source of monotonically-increasing TrapEvent
	// IDs. Atomic so producers can fire ObserveTrap concurrently
	// without coordinating.
	nextID atomic.Uint64

	mu    sync.RWMutex
	rings map[string]*trapRing
}

// NewRecorder returns a Recorder that registers its Prometheus
// collectors against reg and keeps DefaultRingSize trap entries per
// plugin. reg must be non-nil — see newMetricsSet.
func NewRecorder(reg *metrics.Registry) *recorder { //nolint:revive // unexported return is intentional: callers use the Recorder interface.
	return NewRecorderWithRing(reg, DefaultRingSize)
}

// NewRecorderWithRing is like NewRecorder but lets callers (tests,
// mostly) pick the ring capacity. ringSize <= 0 falls back to
// DefaultRingSize.
func NewRecorderWithRing(reg *metrics.Registry, ringSize int) *recorder { //nolint:revive
	if ringSize <= 0 {
		ringSize = DefaultRingSize
	}
	return &recorder{
		metrics:  newMetricsSet(reg),
		ringSize: ringSize,
		rings:    make(map[string]*trapRing),
	}
}

// ObserveInvocation records one hook dispatch — both the counter and
// the histogram are updated. duration is observed in seconds (the
// Prometheus convention). Empty plugin or hook names are tolerated
// but produce a label-shaped warning: the counter still records, but
// the resulting series is hard to reason about.
func (r *recorder) ObserveInvocation(plugin, hook, result string, duration time.Duration) {
	r.metrics.invocations.WithLabelValues(plugin, hook, result).Inc()
	r.metrics.duration.WithLabelValues(plugin, hook).Observe(duration.Seconds())
}

// ObserveTrap records one trap event. The Prometheus counter is
// incremented with the normalised reason; the ring buffer captures
// the full event (including payload) for the "what just broke" view.
func (r *recorder) ObserveTrap(plugin string, reason string, detail TrapDetail) {
	norm := normaliseReason(reason)
	r.metrics.traps.WithLabelValues(plugin, norm).Inc()

	id := r.nextID.Add(1)
	ev := TrapEvent{
		ID:               id,
		Plugin:           plugin,
		Hook:             detail.Hook,
		Reason:           reason,
		NormalisedReason: norm,
		Payload:          detail.Payload,
		At:               time.Now().UTC(),
	}
	r.pushTrap(plugin, ev)
}

// ObserveCapabilityDenied records one capability denial. No ring
// buffer entry is created — denials are already surfaced via the
// audit log (capability.denied event) and don't need an in-memory
// duplicate.
func (r *recorder) ObserveCapabilityDenied(plugin, capability string) {
	r.metrics.capabilityDenials.WithLabelValues(plugin, capability).Inc()
}

// RecentTraps returns the most recent trap events for the plugin in
// reverse-chronological order (newest first). The returned slice is
// a fresh copy; the caller may retain or mutate it without affecting
// the ring.
//
// Unknown plugins return a nil slice (the same shape as a plugin
// that has never trapped).
func (r *recorder) RecentTraps(plugin string) []TrapEvent {
	r.mu.RLock()
	ring, ok := r.rings[plugin]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return ring.snapshot()
}

// FindTrap returns the trap event with the given ID for the plugin,
// or (TrapEvent{}, false) if it is no longer in the ring (or never
// was). The ID is unique per process; if it has already been evicted
// the caller should treat the replay request as expired.
func (r *recorder) FindTrap(plugin string, id uint64) (TrapEvent, bool) {
	r.mu.RLock()
	ring, ok := r.rings[plugin]
	r.mu.RUnlock()
	if !ok {
		return TrapEvent{}, false
	}
	return ring.find(id)
}

// Plugins returns the slugs of every plugin that has recorded at
// least one event (any of the three observation surfaces — not just
// traps). Sorted for determinism. Useful for the admin "list"
// endpoint.
func (r *recorder) Plugins() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.rings))
	for k := range r.rings {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pushTrap adds ev to the plugin's ring buffer, creating the ring on
// first use. Behind a write-lock because the per-plugin ring map can
// grow; the ring itself uses its own mutex for the append.
func (r *recorder) pushTrap(plugin string, ev TrapEvent) {
	r.mu.RLock()
	ring, ok := r.rings[plugin]
	r.mu.RUnlock()
	if ok {
		ring.push(ev)
		return
	}
	r.mu.Lock()
	ring, ok = r.rings[plugin]
	if !ok {
		ring = newTrapRing(r.ringSize)
		r.rings[plugin] = ring
	}
	r.mu.Unlock()
	ring.push(ev)
}

// trapRing is a fixed-capacity circular buffer of TrapEvents. The
// implementation favours simplicity over throughput — the producer
// rate is bounded by the actual plugin trap rate, which in normal
// operation is "zero" and in pathological operation is "a few per
// second". A mutex is plenty.
type trapRing struct {
	mu       sync.Mutex
	capacity int
	buf      []TrapEvent
	// next is the index to write next; wraps modulo capacity.
	next int
	// filled is true once the ring has wrapped at least once; it
	// flips the snapshot ordering from "buf[0..next]" to
	// "buf[next..end] ++ buf[0..next]".
	filled bool
}

func newTrapRing(capacity int) *trapRing {
	return &trapRing{
		capacity: capacity,
		buf:      make([]TrapEvent, capacity),
	}
}

// push appends ev, overwriting the oldest entry once the ring is
// full.
func (r *trapRing) push(ev TrapEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = ev
	r.next = (r.next + 1) % r.capacity
	if r.next == 0 {
		r.filled = true
	}
}

// snapshot returns the events in reverse-chronological order
// (newest first). The returned slice is a fresh copy.
func (r *trapRing) snapshot() []TrapEvent {
	r.mu.Lock()
	defer r.mu.Unlock()

	var n int
	if r.filled {
		n = r.capacity
	} else {
		n = r.next
	}
	out := make([]TrapEvent, 0, n)
	// Walk backwards from the most-recent write (next-1, wrapping).
	for i := 0; i < n; i++ {
		idx := (r.next - 1 - i + r.capacity) % r.capacity
		out = append(out, r.buf[idx])
	}
	return out
}

// find returns the event with the given ID if it is still in the
// ring.
func (r *trapRing) find(id uint64) (TrapEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int
	if r.filled {
		n = r.capacity
	} else {
		n = r.next
	}
	for i := 0; i < n; i++ {
		idx := (r.next - 1 - i + r.capacity) % r.capacity
		if r.buf[idx].ID == id {
			return r.buf[idx], true
		}
	}
	return TrapEvent{}, false
}
