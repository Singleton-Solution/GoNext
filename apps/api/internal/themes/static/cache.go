package static

import (
	"sync"
	"time"
)

// CachedResolver wraps an ActiveResolver with a TTL-based cache so
// repeated CSS requests don't hammer Postgres. The cache is
// invalidated either by TTL expiry or by an explicit Invalidate()
// call (wired from the theme-activate handler in a follow-up).
//
// The implementation favors the read path: the common case is a cache
// hit served under an RLock with no allocation. On miss (or expiry)
// we drop to the write lock, re-check the deadline under it (a peer
// goroutine may have refilled between RUnlock and Lock), and call the
// inner resolver exactly once per expiry window. The inner resolver's
// result — even the empty string the production wiring uses to signal
// "no active theme / read error" — is cached verbatim; we deliberately
// do NOT special-case empty so a transient DB hiccup doesn't degrade
// to "hammer Postgres until it recovers."
//
// Invalidate is best-effort: it resets the deadline to zero so the
// next Get() forces a refill. It does NOT block on an in-flight
// refill; callers (the admin activate handler) treat it as a fire-and
// -forget signal.
type CachedResolver struct {
	inner ActiveResolver
	mu    sync.RWMutex
	value string
	until time.Time
	ttl   time.Duration
	now   func() time.Time // injectable for tests
}

// NewCachedResolver returns a CachedResolver that memoizes the result
// of inner for ttl. A ttl of zero or negative disables caching (every
// Get hits inner) — useful for tests that want to exercise the inner
// call path without rebuilding the wrapper.
func NewCachedResolver(inner ActiveResolver, ttl time.Duration) *CachedResolver {
	return &CachedResolver{
		inner: inner,
		ttl:   ttl,
		now:   time.Now,
	}
}

// Get returns the memoized active-theme slug, refilling from inner if
// the TTL has expired or Invalidate was called since the last refill.
func (c *CachedResolver) Get() string {
	// Fast path: read under RLock. The common case is a cache hit and
	// we want zero contention with peer readers.
	c.mu.RLock()
	if c.ttl > 0 && c.now().Before(c.until) {
		v := c.value
		c.mu.RUnlock()
		return v
	}
	c.mu.RUnlock()

	// Slow path: refill. Take the write lock and re-check the deadline
	// — a peer goroutine may have refilled between our RUnlock and
	// Lock, in which case we serve their cached value rather than
	// piling a second inner call onto the same expiry window.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ttl > 0 && c.now().Before(c.until) {
		return c.value
	}
	c.value = c.inner()
	if c.ttl > 0 {
		c.until = c.now().Add(c.ttl)
	}
	return c.value
}

// Invalidate forces the next Get() call to bypass the cache and
// re-invoke inner. Safe to call concurrently with Get; the worst case
// is a Get that started before Invalidate returns a stale value, but
// the next Get sees the fresh one.
func (c *CachedResolver) Invalidate() {
	c.mu.Lock()
	c.until = time.Time{}
	c.mu.Unlock()
}
