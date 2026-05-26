package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

// BreakerState is the rolled-up status of a plugin's circuit breaker.
//
// Three states track the classic circuit-breaker shape, scoped to the
// host's "auto-deactivate after repeated failures" use case:
//
//   - BreakerClosed   — no trips, or the cool-down window has elapsed
//                       since the last trip. Activate / runtime calls
//                       flow normally.
//   - BreakerOpen     — the plugin has tripped enough times within the
//                       window to cross the threshold. The wired
//                       Manager auto-deactivates the row and refuses
//                       to reactivate until the breaker is reset.
//   - BreakerHalfOpen — reserved for the "one probe call allowed" cut
//                       that ships with the production runtime. Today
//                       the production behaviour is binary
//                       (closed/open); HalfOpen exists in the type
//                       surface so callers don't have to redefine the
//                       enum when probing arrives.
type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// BreakerConfig tunes the trip threshold + cool-down window for a
// single breaker. The defaults match the docs/02-plugin-system.md
// §6.4 "auto-disable after 5 panics in 10 minutes" recommendation —
// aggressive enough that a wedged plugin can't take down the host,
// loose enough that one bad request doesn't disable a healthy plugin.
type BreakerConfig struct {
	// MaxTrips is the failure count that triggers the open state.
	// A breaker rolls the window before counting, so the trip count
	// reflects only the trips that landed within CoolDown. Zero or
	// negative values use defaultMaxTrips.
	MaxTrips int

	// CoolDown is the window inside which trips accumulate. Once
	// CoolDown has elapsed since the oldest tracked trip, that trip
	// is forgotten. Zero or negative values use defaultCoolDown.
	CoolDown time.Duration

	// Now is the clock seam. Tests pin this; production leaves it
	// nil and the breaker uses time.Now().
	Now func() time.Time
}

const (
	defaultMaxTrips = 5
	defaultCoolDown = 10 * time.Minute
)

// Breaker is the in-memory circuit breaker shared by every plugin row
// in the Manager. It maintains one bucket of trip timestamps per slug,
// rolls the window on every Trip() and State() call, and surfaces a
// boolean "tripped this time" the Manager wires into the auto-
// deactivate path.
//
// Concurrency: Trip / Reset / State are safe to call from any
// goroutine. A single sync.Mutex guards the per-slug map; the
// per-slug buckets are protected by the same mutex so reads see a
// consistent count.
//
// The breaker is intentionally process-local: the trip counter does
// NOT survive a restart. A wedged plugin's storage row stays in
// Inactive after auto-deactivate, so the persistence is in the row's
// State; the breaker only carries the in-flight "are we currently
// over threshold" decision.
type Breaker struct {
	cfg BreakerConfig

	mu      sync.Mutex
	buckets map[string]*bucket
}

// bucket is the per-slug state. trips is the timestamps of trips that
// haven't yet aged out of the window; open carries the breaker's
// snapshot at the moment the threshold was crossed, so a subsequent
// State() call returns Open even if the underlying buckets have aged
// out (the operator's Reset() is the only path back to Closed).
type bucket struct {
	trips []time.Time
	open  bool
}

// NewBreaker builds a Breaker with the given configuration. nil cfg
// fields fall back to package defaults. The breaker starts empty;
// every slug's bucket is created lazily on first Trip().
func NewBreaker(cfg BreakerConfig) *Breaker {
	if cfg.MaxTrips <= 0 {
		cfg.MaxTrips = defaultMaxTrips
	}
	if cfg.CoolDown <= 0 {
		cfg.CoolDown = defaultCoolDown
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Breaker{
		cfg:     cfg,
		buckets: make(map[string]*bucket),
	}
}

// Trip records a trip for slug at the current clock instant and
// returns true iff this trip crossed the threshold (closed → open).
// Subsequent trips on an already-open breaker still record into the
// bucket but return false — the host only wants to act once per state
// transition.
//
// Mechanically:
//
//  1. Lock the breaker.
//  2. Append the current time to the slug's bucket.
//  3. Drop trip timestamps older than now() - CoolDown.
//  4. If the bucket size >= MaxTrips and we are not already open,
//     flip to open and return true.
//
// Returning the transition signal here (instead of forcing the caller
// to call State() afterwards) keeps the Manager's auto-deactivate
// path atomic: one Trip call decides whether to fire Deactivate.
func (b *Breaker) Trip(slug string) bool {
	if b == nil || slug == "" {
		return false
	}
	now := b.cfg.Now()

	b.mu.Lock()
	defer b.mu.Unlock()

	bk, ok := b.buckets[slug]
	if !ok {
		bk = &bucket{}
		b.buckets[slug] = bk
	}
	bk.trips = append(bk.trips, now)
	// Roll the window — drop anything older than now - CoolDown.
	cutoff := now.Add(-b.cfg.CoolDown)
	dropped := 0
	for i, t := range bk.trips {
		if t.After(cutoff) {
			dropped = i
			break
		}
		// Loop completed without finding a kept entry — everything
		// is older than cutoff. Drop the whole slice on the next
		// iteration check.
		if i == len(bk.trips)-1 {
			dropped = len(bk.trips)
		}
	}
	if dropped > 0 {
		bk.trips = bk.trips[dropped:]
	}

	if !bk.open && len(bk.trips) >= b.cfg.MaxTrips {
		bk.open = true
		return true
	}
	return false
}

// Reset clears all trip history for slug and returns the breaker to
// the closed state. The operator's "Reset" gesture from the admin UI
// is the wiring; the lifecycle Manager also calls Reset on a
// successful Activate after a recovered failure, so a healed plugin
// gets a clean slate.
func (b *Breaker) Reset(slug string) {
	if b == nil || slug == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.buckets, slug)
}

// State returns the current breaker state for slug. State runs the
// window-roll the same way Trip does so a long-quiet plugin returns
// Closed without a caller's intervention.
//
// State does NOT auto-close an Open breaker — only Reset transitions
// from Open back to Closed. The host wants the operator to inspect
// the failure (and the audit trail) before reactivating a tripped
// plugin, and an Open breaker that quietly self-healed would defeat
// that posture.
func (b *Breaker) State(slug string) BreakerState {
	if b == nil || slug == "" {
		return BreakerClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	bk, ok := b.buckets[slug]
	if !ok {
		return BreakerClosed
	}
	if bk.open {
		return BreakerOpen
	}
	// Roll the window for the read path so a State() check after a
	// long quiet period sees the freshly-empty bucket.
	now := b.cfg.Now()
	cutoff := now.Add(-b.cfg.CoolDown)
	for len(bk.trips) > 0 && !bk.trips[0].After(cutoff) {
		bk.trips = bk.trips[1:]
	}
	if len(bk.trips) == 0 {
		return BreakerClosed
	}
	return BreakerClosed
}

// TripCount returns the number of trips currently in the breaker's
// window for slug. Useful for diagnostic surfaces (admin UI, health
// endpoint) without forcing them to track their own counter.
func (b *Breaker) TripCount(slug string) int {
	if b == nil || slug == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	bk, ok := b.buckets[slug]
	if !ok {
		return 0
	}
	return len(bk.trips)
}

// =============================================================================
// Manager wiring
// =============================================================================

// ErrBreakerOpen is returned by Manager.Activate when the slug's
// breaker is in the Open state. The operator must call Manager.Reset
// (which also resets the breaker) before the plugin can be activated
// again. Distinct from ErrInvalidTransition so the admin UI can
// surface a dedicated "too many crashes" message instead of the
// generic "wrong state" copy.
var ErrBreakerOpen = errors.New("lifecycle: plugin breaker is open; reset required")

// WithBreaker wires a Breaker into the Manager. Trip on the breaker
// auto-deactivates the slug if it crosses the threshold; an Open
// breaker refuses Activate.
//
// Without this option the Manager keeps its legacy behaviour — every
// runtime panic parks the row in Errored and the operator decides
// whether to reactivate. With it, repeated trips inside the window
// also flip the row to Inactive proactively, so a wedged plugin
// can't keep crashing the host loop.
func WithBreaker(b *Breaker) ManagerOption {
	return func(m *Manager) {
		if b != nil {
			m.breaker = b
		}
	}
}

// Trip records a runtime failure for the plugin and, if the breaker
// crosses the threshold, auto-deactivates the row. The auto-deactivate
// reuses the normal Deactivate path so audit + storage + runtime
// teardown all fire as expected.
//
// Trip is safe to call from any goroutine. If the manager has no
// breaker wired, Trip is a no-op — callers don't need to gate the
// call. If the deactivate itself fails (CAS lost, runtime stuck), the
// error is logged and Trip returns it so the caller (host plugin
// dispatch) can decide whether to escalate.
func (m *Manager) Trip(ctx context.Context, slug string) error {
	if m.breaker == nil {
		return nil
	}
	tripped := m.breaker.Trip(slug)
	if !tripped {
		return nil
	}

	current, err := m.storage.Get(ctx, slug)
	if err != nil {
		// Row vanished — there's nothing to deactivate, the breaker
		// snapshot has already flipped to Open so a subsequent
		// Activate will refuse cleanly.
		m.logger.WarnContext(ctx, "lifecycle: Trip auto-deactivate skipped, row not found",
			slog.String("slug", slug), slog.String("err", err.Error()))
		return nil
	}
	// Only Active rows are eligible for the auto-deactivate path —
	// a row that already isn't running has nothing for us to do.
	if current.State != StateActive {
		m.audit(ctx, slug, "plugin.breaker.tripped", audit.SeverityWarning, map[string]any{
			"trip_count": m.breaker.TripCount(slug),
			"action":     "noop_state_not_active",
			"state":      string(current.State),
		})
		return nil
	}

	if err := m.runtime.Unload(ctx, slug); err != nil {
		m.parkErrored(ctx, slug, StateActive, fmt.Sprintf("breaker unload: %v", err))
		return fmt.Errorf("lifecycle: Trip %q: unload: %w", slug, err)
	}
	if err := m.storage.UpdateState(ctx, slug, StateActive, StateInactive, nil); err != nil {
		return fmt.Errorf("lifecycle: Trip %q: deactivate CAS: %w", slug, err)
	}

	m.audit(ctx, slug, "plugin.breaker.tripped", audit.SeverityWarning, map[string]any{
		"trip_count": m.breaker.TripCount(slug),
		"action":     "auto_deactivated",
	})
	return nil
}

// BreakerState returns the breaker's view of slug. Convenience wrapper
// for admin endpoints that don't want to wire the breaker reference
// through their handler tree.
func (m *Manager) BreakerState(slug string) BreakerState {
	if m.breaker == nil {
		return BreakerClosed
	}
	return m.breaker.State(slug)
}

// ResetBreaker clears the breaker's trip history for slug. The admin
// "Reset" gesture wires this in addition to Manager.Reset so a healed
// plugin can be reactivated.
func (m *Manager) ResetBreaker(slug string) {
	if m.breaker == nil {
		return
	}
	m.breaker.Reset(slug)
}
