package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryLimiter is a sync.Map-backed token-bucket limiter intended for
// single-instance use (dev, small self-hosted, embedded tooling). It
// holds state in process memory only — restarts reset all buckets, and
// it does NOT coordinate across replicas. For multi-instance prod, use
// RedisLimiter.
//
// Internally each key gets a bucket struct guarded by a per-bucket
// mutex; the top-level map is a sync.Map for cheap lookup. Buckets are
// retained for the lifetime of the limiter; for long-running processes
// with high-cardinality keys, call PruneIdle periodically.
type MemoryLimiter struct {
	policy Policy

	// now is the time source. Tests inject a deterministic clock by
	// setting now to a func that reads a controlled value. Production
	// uses time.Now.
	now func() time.Time

	buckets sync.Map // key string -> *memBucket
}

// memBucket is the per-key token-bucket state. tokens is stored as a
// float64 because refill is fractional (e.g. a 5/900s policy refills
// 0.0055 tokens per second). Access is serialized by mu so two goroutines
// hitting the same key produce a deterministic result.
type memBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastTick time.Time

	// touched is the last wall-clock time this bucket was consulted.
	// Stored as a Unix nanosecond atomic so PruneIdle can read without
	// taking mu (and racing with Allow).
	touched atomic.Int64
}

// NewMemoryLimiter constructs a MemoryLimiter with the given policy.
// Returns an error if the policy is invalid.
func NewMemoryLimiter(p Policy) (*MemoryLimiter, error) {
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("ratelimit.NewMemoryLimiter: %w", err)
	}
	return &MemoryLimiter{policy: p, now: time.Now}, nil
}

// Allow consumes one token from the bucket associated with key. See
// Limiter for the full contract.
func (l *MemoryLimiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return false, 0, fmt.Errorf("ratelimit.MemoryLimiter.Allow: %w", err)
	}

	b := l.bucketFor(key)
	now := l.now()
	b.touched.Store(now.UnixNano())

	b.mu.Lock()
	defer b.mu.Unlock()

	// Refill since last tick. Cap at Capacity so a long-idle bucket
	// doesn't accumulate unlimited burst.
	if !b.lastTick.IsZero() {
		elapsed := now.Sub(b.lastTick).Seconds()
		b.tokens += elapsed * l.policy.RefillRate
		if b.tokens > float64(l.policy.Capacity) {
			b.tokens = float64(l.policy.Capacity)
		}
	} else {
		// First touch: start with a full bucket. This matches user
		// expectation that a fresh client gets its full burst budget.
		b.tokens = float64(l.policy.Capacity)
	}
	b.lastTick = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0, nil
	}

	// Bucket empty. Retry-After is the time until one more token has
	// accumulated.
	missing := 1.0 - b.tokens
	wait := time.Duration(missing*float64(time.Second)/l.policy.RefillRate) + time.Nanosecond
	return false, wait, nil
}

// Reset clears the bucket associated with key, restoring it to full
// capacity on the next Allow. Used by LoginAttemptLimiter on successful
// login to drop the throttle on a legitimate user.
func (l *MemoryLimiter) Reset(_ context.Context, key string) error {
	l.buckets.Delete(key)
	return nil
}

// PruneIdle removes buckets that haven't been touched in idle. Safe to
// call from a goroutine; it doesn't block Allow on unrelated keys.
//
// Returns the number of buckets pruned.
func (l *MemoryLimiter) PruneIdle(idle time.Duration) int {
	if idle <= 0 {
		return 0
	}
	cutoff := l.now().Add(-idle).UnixNano()
	pruned := 0
	l.buckets.Range(func(k, v any) bool {
		b := v.(*memBucket)
		if b.touched.Load() < cutoff {
			l.buckets.Delete(k)
			pruned++
		}
		return true
	})
	return pruned
}

// bucketFor returns the *memBucket for key, creating one atomically
// if absent. LoadOrStore guarantees only one bucket per key even under
// concurrent first-touches.
func (l *MemoryLimiter) bucketFor(key string) *memBucket {
	if v, ok := l.buckets.Load(key); ok {
		return v.(*memBucket)
	}
	fresh := &memBucket{}
	actual, _ := l.buckets.LoadOrStore(key, fresh)
	return actual.(*memBucket)
}
