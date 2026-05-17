package delivery

import (
	"math/rand/v2"
	"time"
)

// DefaultRetrySchedule is the production retry schedule for webhook
// delivery. It is front-loaded for the common case of a transient blip,
// then stretched to cover a real subscriber outage:
//
//	attempt 1 → wait 30s for attempt 2
//	attempt 2 → wait 5m  for attempt 3
//	attempt 3 → wait 30m for attempt 4
//	attempt 4 → wait 2h  for attempt 5
//	attempt 5 → wait 6h  for attempt 6
//	attempt 6 → wait 24h for attempt 7
//	attempt 7 → exhausted → dead-letter
//
// Total span before dead-letter is ~32h45m — long enough that a
// subscriber's overnight outage doesn't lose the event, short enough
// that a permanently broken URL gets archived within a couple of days.
//
// Issue #266 asks for {30s, 5m, 30m, 2h, 6h, 24h}; this matches verbatim
// while overriding the doc 12 §14.2 sketch (which used 1s/5s/30s/2m/10m/1h/6h —
// front-end too short to survive a real downtime). The newer numbers
// trade some delivery latency for many fewer retries against an
// already-down subscriber.
var DefaultRetrySchedule = []time.Duration{
	30 * time.Second,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// MaxDeliveryAttempts is the number of attempts before a delivery is
// dead-lettered: len(schedule) + 1 (the initial attempt isn't a "retry").
//
// With DefaultRetrySchedule that's 7 attempts. Issue #266 specifies "after
// 6 failures, mark subscription degraded" — six retries after the initial
// attempt = 7 total = matches.
const MaxDeliveryAttempts = 7

// RetryScheduler computes the delay before attempt N+1 given attempt N
// has just failed.
//
// Attempt numbers are 1-based. NextDelay(1) returns the wait before the
// SECOND attempt; NextDelay(MaxDeliveryAttempts) returns zero with
// exhausted=true, telling the caller to dead-letter instead of retry.
//
// Implementations apply full jitter (uniform [0, base)) by default; tests
// can install a deterministic scheduler.
type RetryScheduler interface {
	NextDelay(attempt int) (delay time.Duration, exhausted bool)
}

// Schedule is the standard list-driven RetryScheduler used in production.
// Zero schedule = use DefaultRetrySchedule.
//
// Jitter:
//   - true  (default for NewSchedule): full jitter, delay = U[0, base)
//   - false: returns the schedule value verbatim (used in deterministic
//     unit tests that assert on exact durations)
//
// A non-nil Rand lets tests pin the jitter source. nil uses math/rand/v2's
// global, which is fine for production.
type Schedule struct {
	delays []time.Duration
	jitter bool
	rng    *rand.Rand
}

// NewSchedule returns a Schedule with full jitter enabled. Pass nil to
// adopt DefaultRetrySchedule; pass a slice to customize. The slice is
// copied — later mutations don't affect the Schedule.
func NewSchedule(delays []time.Duration) *Schedule {
	if delays == nil {
		delays = DefaultRetrySchedule
	}
	cp := make([]time.Duration, len(delays))
	copy(cp, delays)
	return &Schedule{delays: cp, jitter: true}
}

// WithoutJitter returns a copy of s with jitter disabled. Used by tests
// that assert the exact NextDelay value.
func (s *Schedule) WithoutJitter() *Schedule {
	if s == nil {
		return nil
	}
	cp := *s
	cp.jitter = false
	return &cp
}

// WithRand returns a copy of s using rng as the jitter source. Allows
// tests to seed a deterministic rand.Rand for reproducible runs.
func (s *Schedule) WithRand(rng *rand.Rand) *Schedule {
	if s == nil {
		return nil
	}
	cp := *s
	cp.rng = rng
	return &cp
}

// Delays returns a copy of the configured delays. The slice is fresh so
// callers can mutate it without affecting the Schedule. Exposed for
// telemetry / admin UI introspection.
func (s *Schedule) Delays() []time.Duration {
	out := make([]time.Duration, len(s.delays))
	copy(out, s.delays)
	return out
}

// NextDelay implements RetryScheduler. Attempt is 1-based.
//
// Returns (0, true) when there is no next attempt (exhausted). Returns
// (delay, false) otherwise; the delay honors jitter if enabled.
func (s *Schedule) NextDelay(attempt int) (time.Duration, bool) {
	if attempt < 1 {
		attempt = 1
	}
	// attempt N just failed; the index into the schedule for the wait
	// before attempt N+1 is N-1 (zero-based).
	idx := attempt - 1
	if idx >= len(s.delays) {
		return 0, true
	}
	d := s.delays[idx]
	if !s.jitter {
		return d, false
	}
	// Full jitter: uniform in [0, d). If d is somehow zero or negative
	// we'd panic on rand.Int63n(0); guard.
	if d <= 0 {
		return 0, false
	}
	n := int64(d)
	var jittered int64
	if s.rng != nil {
		jittered = s.rng.Int64N(n)
	} else {
		jittered = rand.Int64N(n)
	}
	return time.Duration(jittered), false
}

// ConstantSchedule is a RetryScheduler that always returns the same
// delay, never exhausting. Useful only in tests that drive Deliver in a
// tight loop and need a predictable NextDelay. Production callers should
// not use this — it has no dead-letter trigger.
type ConstantSchedule time.Duration

// NextDelay implements RetryScheduler.
func (c ConstantSchedule) NextDelay(int) (time.Duration, bool) {
	return time.Duration(c), false
}
