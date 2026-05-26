package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

// stepClock is a tiny manual clock the breaker tests use to step time
// without sleeping. now() returns the current pinned instant; advance()
// pushes it forward.
type stepClock struct {
	now atomic.Pointer[time.Time]
}

func newStepClock(start time.Time) *stepClock {
	c := &stepClock{}
	c.now.Store(&start)
	return c
}

func (c *stepClock) Now() time.Time {
	p := c.now.Load()
	if p == nil {
		return time.Time{}
	}
	return *p
}

func (c *stepClock) Advance(d time.Duration) {
	prev := c.Now()
	next := prev.Add(d)
	c.now.Store(&next)
}

func TestBreaker_TripsAcrossThresholdFlipsOpen(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b := NewBreaker(BreakerConfig{
		MaxTrips: 3,
		CoolDown: time.Minute,
		Now:      clk.Now,
	})

	if state := b.State("p"); state != BreakerClosed {
		t.Fatalf("fresh breaker should be closed, got %q", state)
	}

	// Two trips: still closed, both Trip() returns false.
	for i := 0; i < 2; i++ {
		if got := b.Trip("p"); got {
			t.Fatalf("trip %d should not flip to open (count below threshold)", i+1)
		}
	}
	if state := b.State("p"); state != BreakerClosed {
		t.Fatalf("breaker after 2 trips should be closed, got %q", state)
	}

	// Third trip crosses MaxTrips=3 → Trip returns true, state flips.
	if got := b.Trip("p"); !got {
		t.Fatal("third trip should flip to open (count == threshold)")
	}
	if state := b.State("p"); state != BreakerOpen {
		t.Fatalf("breaker after 3 trips should be open, got %q", state)
	}

	// Fourth trip on an already-open breaker is still recorded but
	// doesn't return true — the host only wants the single transition.
	if got := b.Trip("p"); got {
		t.Fatal("fourth trip should not re-fire transition on already-open breaker")
	}
}

func TestBreaker_AgesTripsOutOfWindow(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b := NewBreaker(BreakerConfig{
		MaxTrips: 3,
		CoolDown: time.Minute,
		Now:      clk.Now,
	})

	// Land two trips inside the window, then jump past the cool-down
	// before the third — the first two should have aged out, so the
	// third is the only one in the window.
	b.Trip("p")
	b.Trip("p")
	clk.Advance(2 * time.Minute) // both prior trips now older than CoolDown
	if got := b.Trip("p"); got {
		t.Fatal("third trip after window roll must not flip (count rolled back to 1)")
	}
	if state := b.State("p"); state != BreakerClosed {
		t.Fatalf("state should remain closed after window roll, got %q", state)
	}
	if c := b.TripCount("p"); c != 1 {
		t.Fatalf("trip count after window roll: got %d want 1", c)
	}
}

func TestBreaker_ResetClearsHistory(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b := NewBreaker(BreakerConfig{
		MaxTrips: 2,
		CoolDown: time.Hour,
		Now:      clk.Now,
	})
	b.Trip("p")
	b.Trip("p")
	if state := b.State("p"); state != BreakerOpen {
		t.Fatalf("setup precondition failed; state=%q", state)
	}

	b.Reset("p")
	if state := b.State("p"); state != BreakerClosed {
		t.Fatalf("after Reset, state should be closed; got %q", state)
	}
	if c := b.TripCount("p"); c != 0 {
		t.Fatalf("after Reset, trip count should be 0; got %d", c)
	}
}

func TestBreaker_StateNeverAutoClosesOpen(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b := NewBreaker(BreakerConfig{
		MaxTrips: 2,
		CoolDown: time.Minute,
		Now:      clk.Now,
	})
	b.Trip("p")
	b.Trip("p")
	if state := b.State("p"); state != BreakerOpen {
		t.Fatalf("setup precondition failed; state=%q", state)
	}
	clk.Advance(2 * time.Hour) // far past CoolDown
	// State must remain Open — operator's Reset is the only path back.
	if state := b.State("p"); state != BreakerOpen {
		t.Fatalf("State must not auto-close an open breaker; got %q", state)
	}
}

func TestBreaker_PerSlugIsolation(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b := NewBreaker(BreakerConfig{
		MaxTrips: 2,
		CoolDown: time.Hour,
		Now:      clk.Now,
	})

	b.Trip("plugin-a")
	b.Trip("plugin-a") // flips A to open
	if state := b.State("plugin-a"); state != BreakerOpen {
		t.Fatalf("plugin-a should be open, got %q", state)
	}
	if state := b.State("plugin-b"); state != BreakerClosed {
		t.Fatalf("plugin-b should be untouched (closed), got %q", state)
	}
}

func TestBreaker_DefaultsApplied(t *testing.T) {
	b := NewBreaker(BreakerConfig{}) // all zero
	// Use the actual values rather than hard-coding the defaults so a
	// future tweak of the package constants doesn't silently break.
	if b.cfg.MaxTrips != defaultMaxTrips {
		t.Fatalf("default MaxTrips: got %d want %d", b.cfg.MaxTrips, defaultMaxTrips)
	}
	if b.cfg.CoolDown != defaultCoolDown {
		t.Fatalf("default CoolDown: got %v want %v", b.cfg.CoolDown, defaultCoolDown)
	}
	if b.cfg.Now == nil {
		t.Fatal("default Now must be set")
	}
}

// =============================================================================
// Manager wiring tests
// =============================================================================

func TestManager_Trip_AutoDeactivatesAtThreshold(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	breaker := NewBreaker(BreakerConfig{
		MaxTrips: 3,
		CoolDown: time.Hour,
		Now:      clk.Now,
	})
	mgr, storage, _ := newManagerForTest(t, WithBreaker(breaker))

	// Install + activate so the row is Active.
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("activate: %v", err)
	}

	// Two trips under threshold — row stays Active.
	for i := 0; i < 2; i++ {
		if err := mgr.Trip(context.Background(), "gn-seo"); err != nil {
			t.Fatalf("trip %d: %v", i+1, err)
		}
	}
	row, _ := storage.Get(context.Background(), "gn-seo")
	if row.State != StateActive {
		t.Fatalf("after 2 trips, row state: got %q want %q", row.State, StateActive)
	}

	// Third trip crosses the threshold — Manager auto-deactivates.
	if err := mgr.Trip(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("trip 3: %v", err)
	}
	row, _ = storage.Get(context.Background(), "gn-seo")
	if row.State != StateInactive {
		t.Fatalf("after 3rd trip, row state: got %q want %q", row.State, StateInactive)
	}
}

func TestManager_Activate_OpenBreaker_Refused(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	breaker := NewBreaker(BreakerConfig{
		MaxTrips: 2,
		CoolDown: time.Hour,
		Now:      clk.Now,
	})
	mgr, _, _ := newManagerForTest(t, WithBreaker(breaker))

	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("activate (first): %v", err)
	}
	// Trip across threshold → auto-deactivate.
	mgr.Trip(context.Background(), "gn-seo")
	mgr.Trip(context.Background(), "gn-seo")

	// Re-activate must fail with the typed error.
	err := mgr.Activate(context.Background(), "gn-seo")
	if !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("expected ErrBreakerOpen, got: %v", err)
	}
}

func TestManager_Trip_NoBreakerIsNoop(t *testing.T) {
	mgr, storage, _ := newManagerForTest(t)
	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	// Many trips with no breaker wired: row stays Active.
	for i := 0; i < 20; i++ {
		if err := mgr.Trip(context.Background(), "gn-seo"); err != nil {
			t.Fatalf("trip %d: %v", i+1, err)
		}
	}
	row, _ := storage.Get(context.Background(), "gn-seo")
	if row.State != StateActive {
		t.Fatalf("trip without breaker must not deactivate; state=%q", row.State)
	}
}

func TestManager_ResetBreaker_AllowsReactivation(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	breaker := NewBreaker(BreakerConfig{
		MaxTrips: 2,
		CoolDown: time.Hour,
		Now:      clk.Now,
	})
	mgr, _, _ := newManagerForTest(t, WithBreaker(breaker))

	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	mgr.Trip(context.Background(), "gn-seo")
	mgr.Trip(context.Background(), "gn-seo")

	mgr.ResetBreaker("gn-seo")
	if state := mgr.BreakerState("gn-seo"); state != BreakerClosed {
		t.Fatalf("after ResetBreaker, state: got %q want %q", state, BreakerClosed)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("reactivate after reset: %v", err)
	}
}

func TestManager_Trip_AuditEmittedOnAutoDeactivate(t *testing.T) {
	clk := newStepClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	breaker := NewBreaker(BreakerConfig{
		MaxTrips: 1, // any trip flips the breaker
		CoolDown: time.Hour,
		Now:      clk.Now,
	})
	mgr, _, auditStore := newManagerForTest(t, WithBreaker(breaker))

	if _, err := mgr.Install(context.Background(), buildBundle(t, validManifestJSON)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := mgr.Activate(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := mgr.Trip(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("trip: %v", err)
	}

	// Audit row for plugin.breaker.tripped should be present.
	events, err := auditStore.List(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	var found bool
	for _, ev := range events {
		if ev.EventType == "plugin.breaker.tripped" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("plugin.breaker.tripped audit event missing")
	}
}
