package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMemory_RefillMath checks that the token bucket refill arithmetic
// is correct across multiple refill cycles. We pick a low rate so the
// boundary cases are easy to read.
func TestMemory_RefillMath(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_100_000, 0))
	l, err := NewMemoryLimiter(Policy{Capacity: 10, RefillRate: 1}) // 10 burst, 1 tok/sec
	if err != nil {
		t.Fatal(err)
	}
	l.now = clock.Now

	// Drain the bucket completely.
	for i := 0; i < 10; i++ {
		ok, _, _ := l.Allow(context.Background(), "k")
		if !ok {
			t.Fatalf("Allow %d should be allowed (burst capacity)", i)
		}
	}
	if ok, _, _ := l.Allow(context.Background(), "k"); ok {
		t.Fatal("11th call should be denied")
	}

	// Advance 5 seconds → 5 tokens accrue.
	clock.Advance(5 * time.Second)
	for i := 0; i < 5; i++ {
		ok, _, _ := l.Allow(context.Background(), "k")
		if !ok {
			t.Fatalf("after 5s refill, Allow %d should be allowed", i)
		}
	}
	if ok, _, _ := l.Allow(context.Background(), "k"); ok {
		t.Fatal("after consuming 5 refilled tokens, next call should be denied")
	}

	// Advance long enough that the refilled count would exceed capacity
	// (1000 sec * 1 tok/sec = 1000) — verify cap holds.
	clock.Advance(1000 * time.Second)
	for i := 0; i < 10; i++ {
		ok, _, _ := l.Allow(context.Background(), "k")
		if !ok {
			t.Fatalf("after long idle, Allow %d should be allowed (cap at Capacity)", i)
		}
	}
	if ok, _, _ := l.Allow(context.Background(), "k"); ok {
		t.Fatal("bucket should be empty again after full burst")
	}
}

// TestMemory_FractionalRate exercises the 5-per-15-minutes pattern from
// the auth spec (RefillRate ≈ 0.00555 tokens/sec). The bucket should
// allow a 5-burst, deny the 6th, and require ~3 minutes for one token
// to refill.
func TestMemory_FractionalRate(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_200_000, 0))
	policy := Policy{Capacity: 5, RefillRate: 5.0 / (15.0 * 60.0)} // 5 / 15 min
	l, err := NewMemoryLimiter(policy)
	if err != nil {
		t.Fatal(err)
	}
	l.now = clock.Now

	for i := 0; i < 5; i++ {
		ok, _, _ := l.Allow(context.Background(), "user@example.com")
		if !ok {
			t.Fatalf("Allow %d denied prematurely", i)
		}
	}
	ok, retry, _ := l.Allow(context.Background(), "user@example.com")
	if ok {
		t.Fatal("6th attempt should be denied")
	}
	// 1 token at 1/180 tok/s = 180s = 3min
	expected := 180 * time.Second
	if retry < expected-2*time.Second || retry > expected+2*time.Second {
		t.Errorf("expected retryAfter ≈ %v, got %v", expected, retry)
	}

	// Wait exactly the refill time and assert a single token now.
	clock.Advance(retry + time.Millisecond)
	if ok, _, _ := l.Allow(context.Background(), "user@example.com"); !ok {
		t.Fatal("after waiting retryAfter, next call should be allowed")
	}
	if ok, _, _ := l.Allow(context.Background(), "user@example.com"); ok {
		t.Fatal("only one token should have accrued; second call should fail")
	}
}

// TestMemory_Reset clears a bucket so a throttled key gets its full
// burst back.
func TestMemory_Reset(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_300_000, 0))
	l, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 0.01})
	l.now = clock.Now

	if ok, _, _ := l.Allow(context.Background(), "k"); !ok {
		t.Fatal("first call should pass")
	}
	if ok, _, _ := l.Allow(context.Background(), "k"); ok {
		t.Fatal("second call should be denied")
	}

	if err := l.Reset(context.Background(), "k"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if ok, _, _ := l.Allow(context.Background(), "k"); !ok {
		t.Fatal("after Reset, bucket should be full again")
	}
}

// TestMemory_Concurrency confirms the limiter is safe under concurrent
// access and that exactly Capacity allows pass through across N
// goroutines hitting one key at the same instant.
func TestMemory_Concurrency(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_400_000, 0))
	const cap = 50
	l, _ := NewMemoryLimiter(Policy{Capacity: cap, RefillRate: 0.001})
	l.now = clock.Now

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, _ := l.Allow(context.Background(), "race-key")
			if ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != cap {
		t.Errorf("expected exactly %d allowed; got %d", cap, got)
	}
}

// TestMemory_PruneIdle drops idle buckets so the map doesn't grow
// without bound on high-cardinality keys.
func TestMemory_PruneIdle(t *testing.T) {
	clock := newFakeClock(time.Unix(1_700_500_000, 0))
	l, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 1})
	l.now = clock.Now

	// Touch three buckets.
	for _, k := range []string{"a", "b", "c"} {
		if _, _, err := l.Allow(context.Background(), k); err != nil {
			t.Fatal(err)
		}
	}

	// Advance past the idle window and prune.
	clock.Advance(10 * time.Minute)
	pruned := l.PruneIdle(5 * time.Minute)
	if pruned != 3 {
		t.Errorf("expected 3 pruned, got %d", pruned)
	}

	// A pruned bucket starts fresh (full burst again) on next touch.
	if ok, _, _ := l.Allow(context.Background(), "a"); !ok {
		t.Fatal("pruned bucket should accept first call")
	}
}

// TestMemory_ContextCanceled verifies context errors propagate.
func TestMemory_ContextCanceled(t *testing.T) {
	l, _ := NewMemoryLimiter(Policy{Capacity: 1, RefillRate: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := l.Allow(ctx, "k")
	if err == nil {
		t.Fatal("expected context error")
	}
}
