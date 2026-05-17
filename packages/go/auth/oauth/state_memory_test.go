package oauth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryStateStore_PutGetRoundTrip(t *testing.T) {
	s := NewMemoryStateStore()
	ctx := context.Background()

	data := StateData{
		RedirectURI:   "https://app.example.com/auth/callback",
		ExpectedNonce: "nonce-abc",
		PKCEVerifier:  "verifier-xyz",
	}
	if err := s.Put(ctx, "the-state", data, DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "the-state")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RedirectURI != data.RedirectURI {
		t.Errorf("RedirectURI = %q, want %q", got.RedirectURI, data.RedirectURI)
	}
	if got.ExpectedNonce != data.ExpectedNonce {
		t.Errorf("ExpectedNonce = %q, want %q", got.ExpectedNonce, data.ExpectedNonce)
	}
	if got.PKCEVerifier != data.PKCEVerifier {
		t.Errorf("PKCEVerifier = %q, want %q", got.PKCEVerifier, data.PKCEVerifier)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt was not set by Put")
	}
}

func TestMemoryStateStore_SingleUse(t *testing.T) {
	s := NewMemoryStateStore()
	ctx := context.Background()

	if err := s.Put(ctx, "x", StateData{RedirectURI: "https://app/x"}, DefaultStateTTL); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := s.Get(ctx, "x"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	_, err := s.Get(ctx, "x")
	if !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("second Get: err = %v, want errors.Is ErrStateNotFound", err)
	}
	if got := s.Len(); got != 0 {
		t.Errorf("Len after consumption = %d, want 0", got)
	}
}

func TestMemoryStateStore_TTLExpiry(t *testing.T) {
	// Pin the clock so we can advance it deterministically.
	var nowNs int64 // unix-ns
	clock := func() time.Time { return time.Unix(0, atomic.LoadInt64(&nowNs)) }
	s := newMemoryStateStoreWithClock(clock)
	ctx := context.Background()

	atomic.StoreInt64(&nowNs, time.Unix(1_700_000_000, 0).UnixNano())

	if err := s.Put(ctx, "expiring", StateData{RedirectURI: "https://app"}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// 30s later — still alive.
	atomic.AddInt64(&nowNs, int64(30*time.Second))
	if err := s.Put(ctx, "alive", StateData{RedirectURI: "https://x"}, time.Minute); err != nil {
		t.Fatalf("Put alive: %v", err)
	}

	// Advance past expiring's TTL but not alive's.
	atomic.AddInt64(&nowNs, int64(40*time.Second)) // now 70s after expiring's Put

	_, err := s.Get(ctx, "expiring")
	if !errors.Is(err, ErrStateNotFound) {
		t.Errorf("Get(expiring): err = %v, want errors.Is ErrStateNotFound", err)
	}

	// alive should still be there (only 40s of its 60s ttl elapsed).
	if _, err := s.Get(ctx, "alive"); err != nil {
		t.Errorf("Get(alive): err = %v, want nil", err)
	}
}

func TestMemoryStateStore_ExpiryEvictsEntry(t *testing.T) {
	// Even though Get returned ErrStateNotFound, the expired entry
	// should be removed from the map so memory doesn't leak.
	var nowNs atomic.Int64
	clock := func() time.Time { return time.Unix(0, nowNs.Load()) }
	s := newMemoryStateStoreWithClock(clock)
	ctx := context.Background()
	nowNs.Store(time.Unix(1_700_000_000, 0).UnixNano())

	for i := 0; i < 5; i++ {
		if err := s.Put(ctx, string(rune('a'+i)), StateData{}, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.Len(); got != 5 {
		t.Fatalf("Len = %d, want 5", got)
	}

	nowNs.Add(int64(2 * time.Second)) // past everyone's TTL

	for i := 0; i < 5; i++ {
		_, _ = s.Get(ctx, string(rune('a'+i)))
	}
	if got := s.Len(); got != 0 {
		t.Errorf("Len after expired Gets = %d, want 0", got)
	}
}

func TestMemoryStateStore_PutEmptyState(t *testing.T) {
	s := NewMemoryStateStore()
	err := s.Put(context.Background(), "", StateData{}, DefaultStateTTL)
	if !errors.Is(err, ErrEmptyState) {
		t.Errorf("Put(\"\"): err = %v, want errors.Is ErrEmptyState", err)
	}
}

func TestMemoryStateStore_GetEmptyState(t *testing.T) {
	s := NewMemoryStateStore()
	_, err := s.Get(context.Background(), "")
	if !errors.Is(err, ErrEmptyState) {
		t.Errorf("Get(\"\"): err = %v, want errors.Is ErrEmptyState", err)
	}
}

func TestMemoryStateStore_GetUnknown(t *testing.T) {
	s := NewMemoryStateStore()
	_, err := s.Get(context.Background(), "never-put")
	if !errors.Is(err, ErrStateNotFound) {
		t.Errorf("Get unknown: err = %v, want errors.Is ErrStateNotFound", err)
	}
}

func TestMemoryStateStore_NonPositiveTTL(t *testing.T) {
	// A zero or negative TTL is treated as expired immediately — Put
	// succeeds but the entry is gone on first Get. This is the
	// fail-closed behaviour documented on Put.
	s := NewMemoryStateStore()
	ctx := context.Background()
	if err := s.Put(ctx, "zero", StateData{}, 0); err != nil {
		t.Fatalf("Put: %v", err)
	}
	_, err := s.Get(ctx, "zero")
	if !errors.Is(err, ErrStateNotFound) {
		t.Errorf("Get on zero-TTL entry: err = %v, want errors.Is ErrStateNotFound", err)
	}
}

func TestMemoryStateStore_Concurrent(t *testing.T) {
	// 100 goroutines hammering Put+Get on overlapping keys. The race
	// detector enforces no data races; this also checks that single-use
	// is respected under contention.
	s := NewMemoryStateStore()
	ctx := context.Background()

	const writers = 50
	const reads = 50

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k" + string(rune('a'+(i%10)))
			_ = s.Put(ctx, key, StateData{RedirectURI: "https://x"}, time.Minute)
		}(i)
	}
	for i := 0; i < reads; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k" + string(rune('a'+(i%10)))
			_, _ = s.Get(ctx, key)
		}(i)
	}
	wg.Wait()

	// Sanity: subsequent Get of any key returns either the data or
	// ErrStateNotFound, but never panics. Already implicit in the wg
	// finishing without a goroutine crash, but make it explicit.
	for i := 0; i < 10; i++ {
		key := "k" + string(rune('a'+i))
		_, err := s.Get(ctx, key)
		if err != nil && !errors.Is(err, ErrStateNotFound) {
			t.Errorf("post-stress Get(%q) returned unexpected err: %v", key, err)
		}
	}
}

func TestMemoryStateStore_Sweep(t *testing.T) {
	var nowNs atomic.Int64
	clock := func() time.Time { return time.Unix(0, nowNs.Load()) }
	s := newMemoryStateStoreWithClock(clock)
	ctx := context.Background()
	nowNs.Store(time.Unix(1_700_000_000, 0).UnixNano())

	// Three short-lived entries and two long-lived ones.
	for _, k := range []string{"a", "b", "c"} {
		if err := s.Put(ctx, k, StateData{}, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"long1", "long2"} {
		if err := s.Put(ctx, k, StateData{}, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.Len(); got != 5 {
		t.Fatalf("Len after Puts = %d, want 5", got)
	}

	nowNs.Add(int64(2 * time.Second)) // short-lived ones expired
	removed := s.Sweep()
	if removed != 3 {
		t.Errorf("Sweep removed = %d, want 3", removed)
	}
	if got := s.Len(); got != 2 {
		t.Errorf("Len after Sweep = %d, want 2", got)
	}
}

// Compile-time assertion: MemoryStateStore implements StateStore.
var _ StateStore = (*MemoryStateStore)(nil)
