package delivery

import (
	"math/rand/v2"
	"testing"
	"time"
)

// TestSchedule_NoJitterReturnsExactDelays asserts the exact schedule
// per issue #266: 30s, 5m, 30m, 2h, 6h, 24h. If anyone tweaks the
// numbers, this test fails first — the production schedule is a wire
// contract with operators who tune alerts off these durations.
func TestSchedule_NoJitterReturnsExactDelays(t *testing.T) {
	s := NewSchedule(nil).WithoutJitter()
	want := []time.Duration{
		30 * time.Second,
		5 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		6 * time.Hour,
		24 * time.Hour,
	}
	for i, w := range want {
		attempt := i + 1
		got, exhausted := s.NextDelay(attempt)
		if exhausted {
			t.Fatalf("attempt %d: exhausted=true, want false", attempt)
		}
		if got != w {
			t.Fatalf("attempt %d: NextDelay = %s, want %s", attempt, got, w)
		}
	}
	// Attempt 7 (after 6 failures): schedule is exhausted.
	if _, exhausted := s.NextDelay(len(want) + 1); !exhausted {
		t.Fatalf("attempt %d should exhaust the schedule", len(want)+1)
	}
}

// TestSchedule_JitterWithinBounds ensures full-jitter delays land in
// [0, base) and that NextDelay never panics on the bounds.
func TestSchedule_JitterWithinBounds(t *testing.T) {
	src := rand.NewPCG(1, 2)
	s := NewSchedule(nil).WithRand(rand.New(src))
	for attempt := 1; attempt <= MaxDeliveryAttempts-1; attempt++ {
		d, exhausted := s.NextDelay(attempt)
		if exhausted {
			t.Fatalf("attempt %d: unexpectedly exhausted", attempt)
		}
		base := DefaultRetrySchedule[attempt-1]
		if d < 0 || d >= base {
			t.Fatalf("attempt %d: NextDelay = %s, want in [0, %s)", attempt, d, base)
		}
	}
}

// TestSchedule_CustomDelays exercises a caller passing their own
// schedule — the deliverer must honor a custom list, including its
// length (which determines when exhaustion fires).
func TestSchedule_CustomDelays(t *testing.T) {
	delays := []time.Duration{time.Second, 2 * time.Second}
	s := NewSchedule(delays).WithoutJitter()

	d1, _ := s.NextDelay(1)
	if d1 != time.Second {
		t.Fatalf("attempt 1: %v != 1s", d1)
	}
	d2, _ := s.NextDelay(2)
	if d2 != 2*time.Second {
		t.Fatalf("attempt 2: %v != 2s", d2)
	}
	if _, exhausted := s.NextDelay(3); !exhausted {
		t.Fatal("attempt 3 should exhaust a 2-element schedule")
	}
}

func TestSchedule_DelaysReturnsCopy(t *testing.T) {
	s := NewSchedule([]time.Duration{time.Second, 2 * time.Second})
	d := s.Delays()
	d[0] = 999 * time.Hour
	d2 := s.Delays()
	if d2[0] != time.Second {
		t.Fatalf("Delays() should return a defensive copy, got %v", d2)
	}
}

func TestConstantSchedule_NeverExhausts(t *testing.T) {
	c := ConstantSchedule(42 * time.Second)
	for i := 1; i < 100; i++ {
		d, exhausted := c.NextDelay(i)
		if exhausted {
			t.Fatalf("ConstantSchedule exhausted at attempt %d", i)
		}
		if d != 42*time.Second {
			t.Fatalf("ConstantSchedule returned %v, want 42s", d)
		}
	}
}

// TestSchedule_AttemptBelowOneClampedToOne is defensive: a caller that
// hands us a zero or negative attempt should still get a sensible
// answer rather than panicking on a negative slice index.
func TestSchedule_AttemptBelowOneClampedToOne(t *testing.T) {
	s := NewSchedule(nil).WithoutJitter()
	d, exhausted := s.NextDelay(0)
	if exhausted {
		t.Fatal("zero attempt should not exhaust the schedule")
	}
	if d != DefaultRetrySchedule[0] {
		t.Fatalf("got %v, want %v", d, DefaultRetrySchedule[0])
	}
}
