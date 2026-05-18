package migmap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// =============================================================================
// countingStore — wraps a Store to track upstream calls
// =============================================================================

// countingStore decorates any Store and counts how many calls reached
// it. Tests use the counters to assert that a cache hit does NOT
// consult upstream (the whole point of CachedStore) and that a miss
// or a write DOES.
type countingStore struct {
	inner    Store
	getCalls atomic.Int64
	putCalls atomic.Int64
	batch    atomic.Int64
	byTarget atomic.Int64
}

func newCountingStore(inner Store) *countingStore {
	return &countingStore{inner: inner}
}

func (c *countingStore) Put(ctx context.Context, tx Tx, m Mapping) error {
	c.putCalls.Add(1)
	return c.inner.Put(ctx, tx, m)
}
func (c *countingStore) PutBatch(ctx context.Context, tx Tx, ms []Mapping) error {
	c.batch.Add(1)
	return c.inner.PutBatch(ctx, tx, ms)
}
func (c *countingStore) Get(ctx context.Context, s Source, e EntityType, sid string) (*Mapping, bool, error) {
	c.getCalls.Add(1)
	return c.inner.Get(ctx, s, e, sid)
}
func (c *countingStore) GetByTarget(ctx context.Context, t uuid.UUID) ([]Mapping, error) {
	c.byTarget.Add(1)
	return c.inner.GetByTarget(ctx, t)
}

// =============================================================================
// Cache tests
// =============================================================================

func TestCachedStorePutThenGetHitsCache(t *testing.T) {
	t.Parallel()
	// After a write, the matching Get must NOT consult upstream.
	// This is the whole reason the cache exists.
	ctx := context.Background()
	fake := newFakeTx()
	upstream := newCountingStore(NewPostgresStore(fake))
	cache := NewCachedStore(upstream, 16)

	target := uuid.New()
	m := Mapping{Source: SourceWordPress, EntityType: EntityUser, SourceID: "42", TargetID: target}
	if err := cache.Put(ctx, nil, m); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := cache.Get(ctx, SourceWordPress, EntityUser, "42")
	if err != nil || !ok {
		t.Fatalf("Get: err=%v ok=%v", err, ok)
	}
	if got.TargetID != target {
		t.Fatalf("target: got %s, want %s", got.TargetID, target)
	}
	if upstream.getCalls.Load() != 0 {
		t.Fatalf("expected 0 upstream Gets (cache hit), got %d", upstream.getCalls.Load())
	}
}

func TestCachedStoreGetMissesThroughThenCaches(t *testing.T) {
	t.Parallel()
	// Pre-seed the underlying store directly; the first Get through
	// the cache must miss, fetch upstream, and cache the answer. The
	// second Get must hit the cache.
	ctx := context.Background()
	fake := newFakeTx()
	pg := NewPostgresStore(fake)
	target := uuid.New()
	if err := pg.Put(ctx, nil, Mapping{
		Source: SourceWordPress, EntityType: EntityUser,
		SourceID: "42", TargetID: target,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	upstream := newCountingStore(pg)
	cache := NewCachedStore(upstream, 16)

	// First read — should miss the cache and consult upstream.
	if _, ok, _ := cache.Get(ctx, SourceWordPress, EntityUser, "42"); !ok {
		t.Fatal("first Get: miss")
	}
	if upstream.getCalls.Load() != 1 {
		t.Fatalf("first Get: expected 1 upstream call, got %d", upstream.getCalls.Load())
	}
	// Second read — should hit the cache.
	if _, ok, _ := cache.Get(ctx, SourceWordPress, EntityUser, "42"); !ok {
		t.Fatal("second Get: miss")
	}
	if upstream.getCalls.Load() != 1 {
		t.Fatalf("second Get: cache miss (upstream calls = %d, want 1)", upstream.getCalls.Load())
	}
}

func TestCachedStoreNegativeNotCached(t *testing.T) {
	t.Parallel()
	// A "not found" result must NOT be cached. The importer's typical
	// pattern is: Get → miss → insert in DB → Get again. If we cached
	// the negative, the second Get would still report missing.
	ctx := context.Background()
	fake := newFakeTx()
	pg := NewPostgresStore(fake)
	upstream := newCountingStore(pg)
	cache := NewCachedStore(upstream, 16)

	if _, ok, _ := cache.Get(ctx, SourceWordPress, EntityUser, "999"); ok {
		t.Fatal("expected first miss")
	}
	// Insert directly via the underlying store — simulating the
	// importer creating the row out-of-band.
	target := uuid.New()
	if err := pg.Put(ctx, nil, Mapping{
		Source: SourceWordPress, EntityType: EntityUser,
		SourceID: "999", TargetID: target,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Second Get must hit upstream again (negative not cached) and
	// surface the now-present row.
	got, ok, err := cache.Get(ctx, SourceWordPress, EntityUser, "999")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected hit after insert")
	}
	if got.TargetID != target {
		t.Fatalf("target: got %s, want %s", got.TargetID, target)
	}
	if upstream.getCalls.Load() != 2 {
		t.Fatalf("expected 2 upstream Gets (no negative caching), got %d", upstream.getCalls.Load())
	}
}

func TestCachedStoreLRUEviction(t *testing.T) {
	t.Parallel()
	// Capacity 2: insert three items, the first must be evicted.
	// A subsequent Get on the evicted key consults upstream.
	ctx := context.Background()
	fake := newFakeTx()
	pg := NewPostgresStore(fake)
	upstream := newCountingStore(pg)
	cache := NewCachedStore(upstream, 2)

	t1, t2, t3 := uuid.New(), uuid.New(), uuid.New()
	for _, p := range []struct {
		id  string
		uid uuid.UUID
	}{{"1", t1}, {"2", t2}, {"3", t3}} {
		if err := cache.Put(ctx, nil, Mapping{
			Source: SourceWordPress, EntityType: EntityPost,
			SourceID: p.id, TargetID: p.uid,
		}); err != nil {
			t.Fatalf("Put %s: %v", p.id, err)
		}
	}
	if cache.Len() != 2 {
		t.Fatalf("cache length: got %d, want 2", cache.Len())
	}
	// Reading "1" must consult upstream — it was evicted when "3" landed.
	upstream.getCalls.Store(0)
	got, ok, err := cache.Get(ctx, SourceWordPress, EntityPost, "1")
	if err != nil || !ok || got.TargetID != t1 {
		t.Fatalf("Get 1: err=%v ok=%v got=%+v", err, ok, got)
	}
	if upstream.getCalls.Load() != 1 {
		t.Fatalf("expected 1 upstream Get on evicted key, got %d", upstream.getCalls.Load())
	}
}

func TestCachedStoreLRUPromoteOnRead(t *testing.T) {
	t.Parallel()
	// A read on an existing entry must promote it; subsequent
	// insertions evict the OTHER entry, not the recently-touched one.
	ctx := context.Background()
	fake := newFakeTx()
	pg := NewPostgresStore(fake)
	upstream := newCountingStore(pg)
	cache := NewCachedStore(upstream, 2)

	a, b, c := uuid.New(), uuid.New(), uuid.New()
	mustPut(t, cache, "A", a)
	mustPut(t, cache, "B", b)
	// Touch A so it becomes most-recent.
	if _, ok, _ := cache.Get(ctx, SourceWordPress, EntityPost, "A"); !ok {
		t.Fatal("touch A: miss")
	}
	// Insert C — evicts B, not A.
	mustPut(t, cache, "C", c)

	// A must still be in cache.
	upstream.getCalls.Store(0)
	if _, ok, _ := cache.Get(ctx, SourceWordPress, EntityPost, "A"); !ok {
		t.Fatal("A missing after C insert")
	}
	if upstream.getCalls.Load() != 0 {
		t.Fatalf("A should have been cached (recent read); upstream Gets = %d", upstream.getCalls.Load())
	}
	// B must have been evicted.
	if _, ok, _ := cache.Get(ctx, SourceWordPress, EntityPost, "B"); !ok {
		t.Fatal("B missing from upstream (test setup error)")
	}
	if upstream.getCalls.Load() != 1 {
		t.Fatalf("B should have been evicted; expected 1 upstream Get, got %d", upstream.getCalls.Load())
	}
}

func TestCachedStoreUnboundedWhenCapZero(t *testing.T) {
	t.Parallel()
	// Capacity ≤ 0 disables eviction — tests sometimes want this so
	// they can assert "I inserted N, I can read N back" without
	// reasoning about LRU order.
	ctx := context.Background()
	fake := newFakeTx()
	cache := NewCachedStore(NewPostgresStore(fake), 0)

	const n = 100
	for i := 0; i < n; i++ {
		if err := cache.Put(ctx, nil, Mapping{
			Source: SourceWordPress, EntityType: EntityPost,
			SourceID: idStr(i), TargetID: uuid.New(),
		}); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	if cache.Len() != n {
		t.Fatalf("cache length: got %d, want %d", cache.Len(), n)
	}
	_ = ctx
}

func TestCachedStorePutUpdateInPlace(t *testing.T) {
	t.Parallel()
	// Putting the same key twice must NOT grow the cache — the
	// entry is updated in place.
	ctx := context.Background()
	fake := newFakeTx()
	cache := NewCachedStore(NewPostgresStore(fake), 16)

	target := uuid.New()
	for i := 0; i < 5; i++ {
		if err := cache.Put(ctx, nil, Mapping{
			Source: SourceWordPress, EntityType: EntityUser,
			SourceID: "42", TargetID: target,
			Meta: map[string]any{"pass": i},
		}); err != nil {
			t.Fatalf("Put #%d: %v", i, err)
		}
	}
	if cache.Len() != 1 {
		t.Fatalf("expected 1 entry after repeated Put, got %d", cache.Len())
	}
}

func TestCachedStorePutBypassUpstreamFailure(t *testing.T) {
	t.Parallel()
	// If upstream Put fails the cache MUST NOT learn the entry —
	// otherwise the next Get would falsely report a hit.
	ctx := context.Background()
	fake := newFakeTx()
	fake.execErr = errors.New("simulated write failure")
	cache := NewCachedStore(NewPostgresStore(fake), 16)

	err := cache.Put(ctx, nil, Mapping{
		Source: SourceWordPress, EntityType: EntityUser,
		SourceID: "42", TargetID: uuid.New(),
	})
	if err == nil {
		t.Fatal("expected upstream error to propagate")
	}
	if cache.Len() != 0 {
		t.Fatalf("cache poisoned after failed write: len = %d", cache.Len())
	}
}

func TestCachedStorePutWithTxDoesNotCache(t *testing.T) {
	t.Parallel()
	// With an explicit tx, the caller might commit or roll back —
	// the cache must not pre-empt that decision. The next Get must
	// still consult upstream so it sees the post-commit truth.
	ctx := context.Background()
	fake := newFakeTx()
	pg := NewPostgresStore(fake)
	upstream := newCountingStore(pg)
	cache := NewCachedStore(upstream, 16)

	target := uuid.New()
	if err := cache.Put(ctx, fake /* tx */, Mapping{
		Source: SourceWordPress, EntityType: EntityUser,
		SourceID: "42", TargetID: target,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Cache should be empty (we don't trust the pre-commit write).
	if cache.Len() != 0 {
		t.Fatalf("cache should not learn from tx Put; len = %d", cache.Len())
	}
	// Get must hit upstream.
	if _, ok, _ := cache.Get(ctx, SourceWordPress, EntityUser, "42"); !ok {
		t.Fatal("Get: miss after tx Put")
	}
	if upstream.getCalls.Load() != 1 {
		t.Fatalf("expected 1 upstream Get, got %d", upstream.getCalls.Load())
	}
}

func TestCachedStoreConcurrentSafe(t *testing.T) {
	t.Parallel()
	// Concurrent reads + writes against the cache must not race.
	// The race detector enforces the contract; the post-condition
	// just confirms the cache is in a sane state.
	ctx := context.Background()
	fake := newFakeTx()
	cache := NewCachedStore(NewPostgresStore(fake), 8) // small cap to force eviction churn

	const workers = 32
	const ops = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				sid := idStr((w * ops) + i)
				if err := cache.Put(ctx, nil, Mapping{
					Source: SourceWordPress, EntityType: EntityPost,
					SourceID: sid, TargetID: uuid.New(),
				}); err != nil {
					t.Errorf("Put: %v", err)
					return
				}
				_, _, _ = cache.Get(ctx, SourceWordPress, EntityPost, sid)
			}
		}(w)
	}
	wg.Wait()

	// Cap is 8; size after all ops must be ≤ 8.
	if cache.Len() > 8 {
		t.Fatalf("cache exceeded cap: %d > 8", cache.Len())
	}
}

func TestCachedStoreGetByTargetBypassesCache(t *testing.T) {
	t.Parallel()
	// GetByTarget intentionally is not cached. The test asserts the
	// call reaches upstream verbatim.
	ctx := context.Background()
	fake := newFakeTx()
	pg := NewPostgresStore(fake)
	upstream := newCountingStore(pg)
	cache := NewCachedStore(upstream, 16)

	target := uuid.New()
	if err := cache.Put(ctx, nil, Mapping{
		Source: SourceWordPress, EntityType: EntityUser,
		SourceID: "42", TargetID: target,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := cache.GetByTarget(ctx, target)
	if err != nil {
		t.Fatalf("GetByTarget: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if upstream.byTarget.Load() != 1 {
		t.Fatalf("expected upstream GetByTarget == 1, got %d", upstream.byTarget.Load())
	}
}

func TestNewCachedStorePanicsOnNilUpstream(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil upstream")
		}
	}()
	NewCachedStore(nil, 16)
}

// =============================================================================
// Helpers
// =============================================================================

// mustPut is a test helper that fails the test on Put error.
func mustPut(t *testing.T, c *CachedStore, sid string, target uuid.UUID) {
	t.Helper()
	if err := c.Put(context.Background(), nil, Mapping{
		Source: SourceWordPress, EntityType: EntityPost,
		SourceID: sid, TargetID: target,
	}); err != nil {
		t.Fatalf("Put %s: %v", sid, err)
	}
}

// idStr formats a non-negative integer as decimal without going
// through strconv (which would otherwise be the only consumer of
// that package in this test file). Inline base-10 conversion is a
// few lines and keeps imports minimal.
func idStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
