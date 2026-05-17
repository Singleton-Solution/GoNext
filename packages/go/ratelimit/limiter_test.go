package ratelimit

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// limiterFactory builds a fresh Limiter for a contract test, with the
// supplied policy and clock. The teardown returned cleans up backend
// state (Redis FLUSHDB, etc.) so a single test process can run the
// suite against multiple backends sequentially.
type limiterFactory struct {
	name  string
	build func(t *testing.T, p Policy, now func() time.Time) (Limiter, func())
}

// allFactories returns the limiter implementations under contract test.
// Redis is included only when REDIS_URL is set (so unit-test runs
// without infrastructure remain green).
func allFactories(t *testing.T) []limiterFactory {
	t.Helper()
	factories := []limiterFactory{
		{
			name: "Memory",
			build: func(t *testing.T, p Policy, now func() time.Time) (Limiter, func()) {
				l, err := NewMemoryLimiter(p)
				if err != nil {
					t.Fatalf("NewMemoryLimiter: %v", err)
				}
				l.now = now
				return l, func() {}
			},
		},
	}

	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			t.Fatalf("REDIS_URL is set but invalid: %v", err)
		}
		factories = append(factories, limiterFactory{
			name: "Redis",
			build: func(t *testing.T, p Policy, now func() time.Time) (Limiter, func()) {
				if p.Prefix == "" {
					p.Prefix = "ratelimit-test"
				}
				client := redis.NewClient(opts)
				// Ping so we fail fast if the URL is wrong, rather than
				// each Allow call timing out.
				if err := client.Ping(context.Background()).Err(); err != nil {
					t.Skipf("REDIS_URL set but server unreachable: %v", err)
				}
				l, err := NewRedisLimiter(client, p)
				if err != nil {
					t.Fatalf("NewRedisLimiter: %v", err)
				}
				l.now = now

				teardown := func() {
					// Best-effort cleanup: delete keys that match the
					// prefix used by this run.
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					iter := client.Scan(ctx, 0, p.Prefix+":*", 100).Iterator()
					var keys []string
					for iter.Next(ctx) {
						keys = append(keys, iter.Val())
					}
					if len(keys) > 0 {
						client.Del(ctx, keys...)
					}
					_ = client.Close()
				}
				return l, teardown
			},
		})
	}

	return factories
}

// TestLimiterContract is the cross-backend conformance suite. Every
// Limiter implementation must pass these scenarios.
func TestLimiterContract(t *testing.T) {
	for _, f := range allFactories(t) {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Run("FullBurstFirst", func(t *testing.T) { testFullBurstFirst(t, f) })
			t.Run("DenyWhenEmpty", func(t *testing.T) { testDenyWhenEmpty(t, f) })
			t.Run("RetryAfterAdvances", func(t *testing.T) { testRetryAfterAdvances(t, f) })
			t.Run("KeysAreIndependent", func(t *testing.T) { testKeysAreIndependent(t, f) })
		})
	}
}

// testFullBurstFirst verifies a fresh key gets the full capacity as an
// immediate burst before throttling kicks in.
func testFullBurstFirst(t *testing.T, f limiterFactory) {
	t.Helper()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	l, teardown := f.build(t, Policy{Capacity: 5, RefillRate: 1, Prefix: "burst-test"}, clock.Now)
	defer teardown()

	for i := 0; i < 5; i++ {
		ok, _, err := l.Allow(context.Background(), "key-burst")
		if err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("Allow %d: expected allowed at fresh start", i)
		}
	}

	ok, retry, err := l.Allow(context.Background(), "key-burst")
	if err != nil {
		t.Fatalf("Allow 6th: %v", err)
	}
	if ok {
		t.Fatal("expected 6th call to be denied")
	}
	if retry <= 0 {
		t.Fatalf("expected positive retryAfter, got %v", retry)
	}
}

// testDenyWhenEmpty verifies that after exhausting the bucket, the
// next call is denied with a Retry-After roughly equal to the inter-
// token interval.
func testDenyWhenEmpty(t *testing.T, f limiterFactory) {
	t.Helper()
	clock := newFakeClock(time.Unix(1_700_001_000, 0))
	// 1 token / sec means each missing token == 1s of wait.
	l, teardown := f.build(t, Policy{Capacity: 2, RefillRate: 1, Prefix: "empty-test"}, clock.Now)
	defer teardown()

	for i := 0; i < 2; i++ {
		ok, _, err := l.Allow(context.Background(), "k")
		if err != nil || !ok {
			t.Fatalf("Allow %d: ok=%v err=%v", i, ok, err)
		}
	}

	ok, retry, err := l.Allow(context.Background(), "k")
	if err != nil {
		t.Fatalf("Allow over: %v", err)
	}
	if ok {
		t.Fatal("expected denial")
	}
	// At 1 tok/sec, retryAfter should be ~1s (Redis rounds up to
	// whole milliseconds; allow some slack).
	if retry < 900*time.Millisecond || retry > 1100*time.Millisecond {
		t.Errorf("retryAfter outside expected band: %v", retry)
	}
}

// testRetryAfterAdvances verifies that after waiting (clock advance)
// for the refill period, the next call is allowed.
func testRetryAfterAdvances(t *testing.T, f limiterFactory) {
	t.Helper()
	clock := newFakeClock(time.Unix(1_700_002_000, 0))
	l, teardown := f.build(t, Policy{Capacity: 1, RefillRate: 2, Prefix: "refill-test"}, clock.Now)
	defer teardown()

	ok, _, err := l.Allow(context.Background(), "k")
	if err != nil || !ok {
		t.Fatalf("first call: ok=%v err=%v", ok, err)
	}

	ok, _, err = l.Allow(context.Background(), "k")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if ok {
		t.Fatal("expected denial immediately after burst")
	}

	// Advance 600 ms: with rate 2/sec, that's 1.2 tokens refilled,
	// well over 1, so next Allow must pass.
	clock.Advance(600 * time.Millisecond)

	ok, _, err = l.Allow(context.Background(), "k")
	if err != nil {
		t.Fatalf("post-refill: %v", err)
	}
	if !ok {
		t.Fatal("expected allowed after refill window")
	}
}

// testKeysAreIndependent verifies one key being throttled doesn't
// affect another's bucket.
func testKeysAreIndependent(t *testing.T, f limiterFactory) {
	t.Helper()
	clock := newFakeClock(time.Unix(1_700_003_000, 0))
	l, teardown := f.build(t, Policy{Capacity: 1, RefillRate: 0.1, Prefix: "iso-test"}, clock.Now)
	defer teardown()

	if ok, _, err := l.Allow(context.Background(), "alice"); err != nil || !ok {
		t.Fatalf("alice first: ok=%v err=%v", ok, err)
	}
	if ok, _, _ := l.Allow(context.Background(), "alice"); ok {
		t.Fatal("alice should be throttled after first burst")
	}
	if ok, _, err := l.Allow(context.Background(), "bob"); err != nil || !ok {
		t.Fatalf("bob first: ok=%v err=%v", ok, err)
	}
}

// fakeClock is a deterministic time source used by tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// TestPolicyValidate covers the malformed-policy paths.
func TestPolicyValidate(t *testing.T) {
	cases := []struct {
		name string
		p    Policy
		ok   bool
	}{
		{"zero capacity", Policy{Capacity: 0, RefillRate: 1}, false},
		{"negative capacity", Policy{Capacity: -1, RefillRate: 1}, false},
		{"zero rate", Policy{Capacity: 1, RefillRate: 0}, false},
		{"negative rate", Policy{Capacity: 1, RefillRate: -1}, false},
		{"valid", Policy{Capacity: 1, RefillRate: 1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.validate()
			if tc.ok && err != nil {
				t.Errorf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
