package static

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newCounter returns an ActiveResolver that records call count and
// returns the given slug on every invocation. Tests inspect the
// counter to assert how many times the inner resolver was actually
// hit — the whole point of the cache is to keep this number small.
func newCounter(slug string) (ActiveResolver, *int64) {
	var n int64
	return func() string {
		atomic.AddInt64(&n, 1)
		return slug
	}, &n
}

func TestCachedResolver_HitReturnsMemoizedValue(t *testing.T) {
	inner, calls := newCounter("gn-hello")
	c := NewCachedResolver(inner, time.Minute)

	// First call populates the cache (inner fires once).
	if got := c.Get(); got != "gn-hello" {
		t.Fatalf("first Get: got %q, want gn-hello", got)
	}
	// Subsequent calls within the TTL must not hit inner.
	for i := 0; i < 100; i++ {
		if got := c.Get(); got != "gn-hello" {
			t.Fatalf("iter %d: got %q, want gn-hello", i, got)
		}
	}
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Errorf("inner calls: got %d, want 1 (100 cached reads should not refill)", n)
	}
}

func TestCachedResolver_ExpiresAfterTTL(t *testing.T) {
	inner, calls := newCounter("gn-hello")
	c := NewCachedResolver(inner, 100*time.Millisecond)

	// Inject a controllable clock so the test is deterministic and
	// doesn't actually sleep. The cache uses c.now() everywhere.
	var nowVal time.Time
	c.now = func() time.Time { return nowVal }
	nowVal = time.Unix(1_000_000, 0)

	// First call: cold cache, fires inner, sets until = now + ttl.
	if got := c.Get(); got != "gn-hello" {
		t.Fatalf("first Get: got %q, want gn-hello", got)
	}
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Fatalf("after first Get: inner calls = %d, want 1", n)
	}

	// Advance clock half the TTL — still inside the window, no refill.
	nowVal = nowVal.Add(50 * time.Millisecond)
	if got := c.Get(); got != "gn-hello" {
		t.Fatalf("mid-ttl Get: got %q", got)
	}
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Errorf("mid-ttl: inner calls = %d, want 1 (still cached)", n)
	}

	// Advance past the TTL — next Get must refill.
	nowVal = nowVal.Add(200 * time.Millisecond)
	if got := c.Get(); got != "gn-hello" {
		t.Fatalf("post-ttl Get: got %q", got)
	}
	if n := atomic.LoadInt64(calls); n != 2 {
		t.Errorf("post-ttl: inner calls = %d, want 2 (refill on expiry)", n)
	}
}

func TestCachedResolver_InvalidateForcesRefill(t *testing.T) {
	inner, calls := newCounter("gn-hello")
	c := NewCachedResolver(inner, time.Hour)

	// Populate the cache.
	_ = c.Get()
	_ = c.Get()
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Fatalf("after 2 Gets: inner calls = %d, want 1", n)
	}

	// Invalidate, then Get — must hit inner.
	c.Invalidate()
	_ = c.Get()
	if n := atomic.LoadInt64(calls); n != 2 {
		t.Errorf("after Invalidate + Get: inner calls = %d, want 2", n)
	}

	// Subsequent Gets within the (refreshed) TTL are cached again.
	_ = c.Get()
	_ = c.Get()
	if n := atomic.LoadInt64(calls); n != 2 {
		t.Errorf("after Invalidate + 3 Gets: inner calls = %d, want 2", n)
	}
}

func TestCachedResolver_InvalidateBeforeFirstGet(t *testing.T) {
	// Invalidate on a fresh resolver must not crash and must not
	// confuse the first Get: it should still hit inner exactly once
	// and then memoize.
	inner, calls := newCounter("gn-hello")
	c := NewCachedResolver(inner, time.Minute)

	c.Invalidate()
	if got := c.Get(); got != "gn-hello" {
		t.Fatalf("first Get after Invalidate: got %q", got)
	}
	_ = c.Get()
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Errorf("inner calls = %d, want 1", n)
	}
}

func TestCachedResolver_ZeroTTLAlwaysHitsInner(t *testing.T) {
	// A non-positive TTL disables caching — every Get calls inner.
	// Useful for callers who want to opt out without rebuilding the
	// dependency wiring.
	inner, calls := newCounter("gn-hello")
	c := NewCachedResolver(inner, 0)

	for i := 0; i < 5; i++ {
		if got := c.Get(); got != "gn-hello" {
			t.Fatalf("iter %d: got %q", i, got)
		}
	}
	if n := atomic.LoadInt64(calls); n != 5 {
		t.Errorf("inner calls = %d, want 5 (ttl=0 disables cache)", n)
	}
}

func TestCachedResolver_CachesEmptyValue(t *testing.T) {
	// The production wiring returns "" on a DB read error and the
	// handler treats that as "no active theme → 404". We deliberately
	// cache the empty string for the TTL so a transient DB hiccup
	// doesn't degrade to "hammer Postgres on every request until it
	// recovers." Invalidate is the explicit escape hatch.
	inner, calls := newCounter("")
	c := NewCachedResolver(inner, time.Minute)

	for i := 0; i < 10; i++ {
		if got := c.Get(); got != "" {
			t.Fatalf("iter %d: got %q, want empty", i, got)
		}
	}
	if n := atomic.LoadInt64(calls); n != 1 {
		t.Errorf("inner calls = %d, want 1 (empty values cache too)", n)
	}
}

func TestCachedResolver_ConcurrentAccess(t *testing.T) {
	// Race-detector workout: many goroutines hit Get concurrently with
	// occasional Invalidate calls. We don't assert an exact inner-
	// call count (the timing is racy by design) — the contract is
	//   (a) every Get returns the configured slug, and
	//   (b) the inner count stays in a sane bound.
	// Run with `go test -race` to catch any unguarded access.
	inner, calls := newCounter("gn-hello")
	c := NewCachedResolver(inner, 10*time.Millisecond)

	const readers = 64
	const itersPerReader = 500

	var wg sync.WaitGroup
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < itersPerReader; j++ {
				if got := c.Get(); got != "gn-hello" {
					t.Errorf("reader %d iter %d: got %q", i, j, got)
					return
				}
				// One in 50 iterations, force an invalidation. This
				// mimics the production pattern where the admin
				// activate handler signals a switch — rare relative
				// to public CSS reads.
				if j%50 == 0 {
					c.Invalidate()
				}
			}
		}(i)
	}
	wg.Wait()

	// Sanity check: the inner resolver must have been called at least
	// once (cold start) but well below the total Get count (caching
	// should have absorbed the vast majority). The exact upper bound
	// depends on goroutine scheduling and the 10ms TTL; 64*500=32000
	// total Gets, and inner should have fired at most a few hundred
	// times. We pick a generous ceiling so the test isn't flaky on a
	// slow CI box.
	total := int64(readers * itersPerReader)
	got := atomic.LoadInt64(calls)
	if got < 1 {
		t.Errorf("inner never called (counter = %d)", got)
	}
	if got >= total {
		t.Errorf("cache ineffective: inner called %d / %d times", got, total)
	}
}

func TestCachedResolver_TracksValueChangesAfterInvalidate(t *testing.T) {
	// The inner resolver returns a different slug on each call. The
	// cache should pin the first value until Invalidate fires, then
	// pick up the next inner result.
	var n int64
	inner := func() string {
		v := atomic.AddInt64(&n, 1)
		if v == 1 {
			return "gn-hello"
		}
		return "gn-pro"
	}
	c := NewCachedResolver(inner, time.Hour)

	if got := c.Get(); got != "gn-hello" {
		t.Fatalf("first Get: got %q, want gn-hello", got)
	}
	if got := c.Get(); got != "gn-hello" {
		t.Errorf("cached Get: got %q, want gn-hello (still cached)", got)
	}

	c.Invalidate()
	if got := c.Get(); got != "gn-pro" {
		t.Errorf("post-invalidate Get: got %q, want gn-pro", got)
	}
}
