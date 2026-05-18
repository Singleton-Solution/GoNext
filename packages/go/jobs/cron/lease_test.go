package cron

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newMiniRedis spins up an in-process Redis (miniredis) and returns a
// go-redis client connected to it plus the *miniredis.Miniredis handle
// (callers use FastForward to advance time deterministically).
// Cleanup tears down both via t.Cleanup.
func newMiniRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

// TestNewLease_ValidationErrors pins the construction-time checks.
// Each missing/invalid field must surface a typed error so a misconfigured
// boot fails loudly rather than at first Acquire.
func TestNewLease_ValidationErrors(t *testing.T) {
	t.Parallel()
	rdb, _ := newMiniRedis(t)

	cases := []struct {
		name string
		key  string
		own  string
		ttl  time.Duration
		want error
	}{
		{"empty-key", "", "owner", time.Second, ErrEmptyKey},
		{"empty-owner", "k", "", time.Second, ErrEmptyOwner},
		{"zero-ttl", "k", "o", 0, ErrInvalidTTL},
		{"negative-ttl", "k", "o", -time.Second, ErrInvalidTTL},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewLease(rdb, tc.key, tc.own, tc.ttl)
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewLease: got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestLease_AcquireRenewRelease exercises the full happy-path round
// trip. The same Owner can Acquire, Renew, Release in sequence; a
// second Acquire on the same key returns false (already held) until
// Release.
func TestLease_AcquireRenewRelease(t *testing.T) {
	t.Parallel()
	rdb, _ := newMiniRedis(t)
	ctx := context.Background()

	l, err := NewLease(rdb, "k", "owner-a", 5*time.Second)
	if err != nil {
		t.Fatalf("NewLease: %v", err)
	}

	got, err := l.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !got {
		t.Fatal("Acquire: want true on fresh key")
	}

	if err := l.Renew(ctx); err != nil {
		t.Fatalf("Renew: %v", err)
	}

	// Second Acquire by the same lease must observe the existing key.
	got, err = l.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire after Acquire: %v", err)
	}
	if got {
		t.Fatal("Acquire after Acquire: want false (already held)")
	}

	if err := l.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// After release the key is gone; Renew is now ErrNotLeader.
	if err := l.Renew(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Renew after Release: got %v, want ErrNotLeader", err)
	}
}

// TestLease_TwoOwnersCompete exercises the SET NX contract: only one
// of two concurrent Acquire calls succeeds. The losing owner observes
// (false, nil) — a contented lease is not an error.
func TestLease_TwoOwnersCompete(t *testing.T) {
	t.Parallel()
	rdb, _ := newMiniRedis(t)
	ctx := context.Background()

	la, _ := NewLease(rdb, "k", "owner-a", 5*time.Second)
	lb, _ := NewLease(rdb, "k", "owner-b", 5*time.Second)

	gotA, err := la.Acquire(ctx)
	if err != nil {
		t.Fatalf("la.Acquire: %v", err)
	}
	gotB, err := lb.Acquire(ctx)
	if err != nil {
		t.Fatalf("lb.Acquire: %v", err)
	}
	if !gotA || gotB {
		t.Fatalf("Acquire: gotA=%v gotB=%v, want gotA=true gotB=false", gotA, gotB)
	}
}

// TestLease_RenewByWrongOwnerRejected pins the compare-and-swap
// guard: a stale Owner attempting Renew cannot extend a key it
// doesn't own. Without this guard, a process whose lease had silently
// expired (and was reacquired by someone else) could push the new
// holder's TTL forward.
func TestLease_RenewByWrongOwnerRejected(t *testing.T) {
	t.Parallel()
	rdb, _ := newMiniRedis(t)
	ctx := context.Background()

	holder, _ := NewLease(rdb, "k", "owner-a", 5*time.Second)
	stale, _ := NewLease(rdb, "k", "owner-b", 5*time.Second)

	if ok, err := holder.Acquire(ctx); err != nil || !ok {
		t.Fatalf("holder.Acquire: ok=%v err=%v", ok, err)
	}
	if err := stale.Renew(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("stale.Renew: got %v, want ErrNotLeader", err)
	}
	if err := stale.Release(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("stale.Release: got %v, want ErrNotLeader", err)
	}
}

// TestLease_RenewMissingKey covers the "we expired before Renew got
// to us" path: GET returns nil, the script returns 0, the Go wrapper
// translates to ErrNotLeader.
func TestLease_RenewMissingKey(t *testing.T) {
	t.Parallel()
	rdb, _ := newMiniRedis(t)
	ctx := context.Background()
	l, _ := NewLease(rdb, "k", "owner", 5*time.Second)
	if err := l.Renew(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Renew on absent key: got %v, want ErrNotLeader", err)
	}
}

// TestLease_TTLExpiry exercises the Redis TTL path via miniredis's
// FastForward: a lease with a 1s TTL is gone after we forward 2s.
// This is the test that exercises the "leader dies without releasing"
// recovery path from a unit-test angle (we simulate the death by
// not running the leader's renew loop).
func TestLease_TTLExpiry(t *testing.T) {
	t.Parallel()
	rdb, mr := newMiniRedis(t)
	ctx := context.Background()

	dead, _ := NewLease(rdb, "k", "dead-leader", time.Second)
	fresh, _ := NewLease(rdb, "k", "fresh-leader", time.Second)

	if ok, err := dead.Acquire(ctx); err != nil || !ok {
		t.Fatalf("dead.Acquire: %v", err)
	}
	if ok, err := fresh.Acquire(ctx); err != nil || ok {
		t.Fatalf("fresh.Acquire while dead holds: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	mr.FastForward(2 * time.Second)
	if ok, err := fresh.Acquire(ctx); err != nil || !ok {
		t.Fatalf("fresh.Acquire after TTL: ok=%v err=%v, want ok=true err=nil", ok, err)
	}
}

// TestLease_CurrentOwner sanity-checks the diagnostic helper. The
// scheduler exposes a Prometheus gauge from this value; the value
// must be the literal Owner string, not a hash or a JSON blob.
func TestLease_CurrentOwner(t *testing.T) {
	t.Parallel()
	rdb, _ := newMiniRedis(t)
	ctx := context.Background()

	l, _ := NewLease(rdb, "k", "owner-x", 5*time.Second)
	if _, err := l.CurrentOwner(ctx); !errors.Is(err, redis.Nil) {
		t.Fatalf("CurrentOwner on empty key: got %v, want redis.Nil", err)
	}
	if ok, err := l.Acquire(ctx); err != nil || !ok {
		t.Fatalf("Acquire: %v", err)
	}
	got, err := l.CurrentOwner(ctx)
	if err != nil {
		t.Fatalf("CurrentOwner: %v", err)
	}
	if got != "owner-x" {
		t.Fatalf("CurrentOwner: got %q, want %q", got, "owner-x")
	}
}

// TestLease_ConcurrentAcquireExactlyOneWins runs N goroutines racing
// for the same key. Exactly one Acquire must return true; the rest
// must observe (false, nil). The race detector backs this up by
// failing if any of the lease internals carry an unsynchronized
// state mutation.
func TestLease_ConcurrentAcquireExactlyOneWins(t *testing.T) {
	t.Parallel()
	rdb, _ := newMiniRedis(t)
	ctx := context.Background()

	const n = 16
	leases := make([]*Lease, n)
	for i := 0; i < n; i++ {
		l, err := NewLease(rdb, "k", "owner-"+itoa(i), 5*time.Second)
		if err != nil {
			t.Fatalf("NewLease: %v", err)
		}
		leases[i] = l
	}

	var wg sync.WaitGroup
	wg.Add(n)
	var wins int32
	var winsMu sync.Mutex
	for i := 0; i < n; i++ {
		l := leases[i]
		go func() {
			defer wg.Done()
			ok, err := l.Acquire(ctx)
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			if ok {
				winsMu.Lock()
				wins++
				winsMu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("Acquire winners: got %d, want 1", wins)
	}
}

// itoa is a tiny helper to keep the lease-naming loop inline-free.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = digits[i%10]
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
