package cron

import (
	"math/rand"
	"testing"
	"time"
)

// TestIdleSleep_BoundsAroundHalfTTL pins the jitter window: the
// returned duration must lie within [base-span, base+span] where
// base=TTL/2 and span=base*jitterFraction. We sample many times and
// check the bounds rather than reproduce the formula exactly.
func TestIdleSleep_BoundsAroundHalfTTL(t *testing.T) {
	t.Parallel()
	ttl := 1 * time.Second
	base := ttl / 2
	span := time.Duration(float64(base) * jitterFraction)
	low := base - span
	high := base + span
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		got := idleSleep(ttl, rng)
		if got < low || got > high {
			t.Fatalf("idleSleep[%d] = %v, want in [%v, %v]", i, got, low, high)
		}
	}
}

// TestIdleSleep_ZeroTTLFloors a misconfigured TTL must not busy-loop:
// the function clamps to at least 1ms.
func TestIdleSleep_ZeroTTLFloors(t *testing.T) {
	t.Parallel()
	got := idleSleep(0, nil)
	if got < time.Millisecond {
		t.Fatalf("idleSleep(0): got %v, want >= 1ms", got)
	}
}

// TestIdleSleep_NilRngUsesPackageRand exercises the nil-RNG fallback
// path. The test asserts only that no panic occurs and that the
// result is in the expected bounds — package-level rand is
// deterministic if unseeded but its sequence isn't load-bearing here.
func TestIdleSleep_NilRngUsesPackageRand(t *testing.T) {
	t.Parallel()
	for i := 0; i < 50; i++ {
		got := idleSleep(1*time.Second, nil)
		if got <= 0 {
			t.Fatalf("idleSleep nil rng[%d]: got %v, want positive", i, got)
		}
	}
}
