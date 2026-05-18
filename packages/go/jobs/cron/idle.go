package cron

import (
	"math/rand"
	"time"
)

// jitterFraction is the symmetric jitter window applied to the
// idle-poll cadence. With a TTL of 15s and the default poll cadence
// of TTL/2 (7.5s), a fraction of 0.25 means each non-leader sleeps
// in the range [5.625s, 9.375s] before retrying Acquire. The point
// is to break the thundering herd that would otherwise see every
// non-leader retry simultaneously at the TTL boundary.
const jitterFraction = 0.25

// idleSleep returns the next sleep duration for a non-leader replica.
// The base is ttl/2 — half the lease window — so a leader death is
// noticed within at most (ttl + idleSleep) which on the operational
// path is well under one cron cadence.
//
// The fraction `jitterFraction` of base is added or subtracted at
// random so contending followers desynchronize across cycles. The
// rand source is the package-default — we don't seed it explicitly;
// even an unseeded source produces enough variety across goroutines
// to break the herd.
//
// idleSleep guarantees the returned duration is at least 1
// millisecond, so a misconfigured TTL of zero doesn't busy-loop the
// follower.
func idleSleep(ttl time.Duration, rng *rand.Rand) time.Duration {
	base := ttl / 2
	if base <= 0 {
		return time.Millisecond
	}
	span := float64(base) * jitterFraction
	// rng can be nil in unit tests that don't construct one; fall
	// back to the global source. We deliberately do NOT seed the
	// global source here — production callers should construct
	// their own *rand.Rand with a time-based seed in NewScheduler.
	var delta float64
	if rng == nil {
		delta = (rand.Float64()*2 - 1) * span
	} else {
		delta = (rng.Float64()*2 - 1) * span
	}
	out := time.Duration(float64(base) + delta)
	if out < time.Millisecond {
		out = time.Millisecond
	}
	return out
}
