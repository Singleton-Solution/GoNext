package migmap

import (
	"container/list"
	"context"
	"sync"

	"github.com/google/uuid"
)

// =============================================================================
// CachedStore
// =============================================================================

// CachedStore wraps an underlying [Store] with an in-memory LRU. The
// importer runs in a single process for the duration of a single
// import; during that run the same user or term mapping is read
// thousands of times (every post in the export touches the same
// author). The LRU keeps those reads off Postgres.
//
// Semantics:
//
//   - Get is READ-THROUGH. A hit returns the cached mapping; a miss
//     consults the underlying Store and (on a positive result) caches
//     the answer. We do NOT cache negative results — a "not found"
//     during pass 1 means "the importer will create this in a moment,"
//     and a stale negative would force a second DB round-trip after
//     the create.
//
//   - Put / PutBatch are WRITE-THROUGH. The write hits the underlying
//     Store first; only on success is the cache updated. This way a
//     failed insert (e.g. the row violates a downstream FK) leaves
//     the cache in lockstep with the DB.
//
//   - GetByTarget is NOT cached. The reverse lookup is rare and is
//     used by diagnostics, not the hot path. Caching it would also
//     require tracking targetID → keys mappings, doubling the
//     bookkeeping for negligible gain.
//
// The cache is safe for concurrent use. The LRU bookkeeping (list
// pointers, map indirection) is guarded by a single mutex — fine for
// the import workload, where worker count is small (a few dozen) and
// per-call work dominates the lock-acquire cost.
type CachedStore struct {
	upstream Store
	cap      int

	mu    sync.Mutex
	items map[cacheKey]*list.Element
	order *list.List // front = most recently used
}

// cacheKey is the lookup tuple. We collapse the three TEXT columns
// into a single value because map[struct]... is cheaper than nested
// maps and the struct compares by value.
type cacheKey struct {
	source     Source
	entityType EntityType
	sourceID   string
}

// cacheEntry is what we actually store in the list. Keeping the key
// alongside the value means eviction can clear items[key] without a
// reverse map.
type cacheEntry struct {
	key   cacheKey
	value Mapping
}

// NewCachedStore wraps upstream with an LRU sized at capacity. A
// capacity of 0 or less disables eviction — the cache grows
// unboundedly, which is sometimes what tests want. Production
// callers should pass a positive integer (the WXR importer in #144
// uses 10_000, which comfortably holds every user/term in a typical
// blog export).
//
// upstream must not be nil; we panic on construction rather than at
// the first Get to surface the mistake near the offending caller.
func NewCachedStore(upstream Store, capacity int) *CachedStore {
	if upstream == nil {
		panic("migmap.NewCachedStore: upstream is nil")
	}
	return &CachedStore{
		upstream: upstream,
		cap:      capacity,
		items:    make(map[cacheKey]*list.Element),
		order:    list.New(),
	}
}

// Put writes through to upstream then caches the mapping. We do the
// write first so a transport failure leaves the cache untouched —
// callers retrying on error must not see a phantom hit before the
// underlying row exists.
func (c *CachedStore) Put(ctx context.Context, tx Tx, m Mapping) error {
	if err := c.upstream.Put(ctx, tx, m); err != nil {
		return err
	}
	// Only cache on a real transaction-less write. When tx is
	// non-nil, the write may still be rolled back by the caller; we
	// can't observe Commit/Rollback from here. Caching pre-commit
	// would expose a row that the DB might never have. The importer
	// accepts this — for the common case (a Tx that commits) the
	// next Get pulls the value from Postgres and warms the cache
	// then.
	if tx == nil {
		c.cacheStore(m)
	}
	return nil
}

// PutBatch behaves like [Put] for each row.
func (c *CachedStore) PutBatch(ctx context.Context, tx Tx, ms []Mapping) error {
	if err := c.upstream.PutBatch(ctx, tx, ms); err != nil {
		return err
	}
	if tx == nil {
		for _, m := range ms {
			c.cacheStore(m)
		}
	}
	return nil
}

// Get returns a cached hit if present, otherwise consults upstream
// and caches a positive result.
func (c *CachedStore) Get(ctx context.Context, source Source, entityType EntityType, sourceID string) (*Mapping, bool, error) {
	k := cacheKey{source: source, entityType: entityType, sourceID: sourceID}
	if hit, ok := c.cacheLoad(k); ok {
		// Return a copy so the caller can mutate the meta map
		// without affecting future Gets — the cache stores the
		// authoritative version.
		copy := hit
		return &copy, true, nil
	}
	m, ok, err := c.upstream.Get(ctx, source, entityType, sourceID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	c.cacheStore(*m)
	// Return a fresh copy too, for the same isolation reason as above.
	out := *m
	return &out, true, nil
}

// GetByTarget bypasses the cache and consults upstream directly.
// See the type godoc for the rationale.
func (c *CachedStore) GetByTarget(ctx context.Context, targetID uuid.UUID) ([]Mapping, error) {
	return c.upstream.GetByTarget(ctx, targetID)
}

// =============================================================================
// LRU internals
// =============================================================================

// cacheLoad returns the cached mapping for k and promotes it to the
// front of the LRU. The mutex is held for the duration of the lookup
// because list.MoveToFront mutates the list pointers.
func (c *CachedStore) cacheLoad(k cacheKey) (Mapping, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return Mapping{}, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry).value, true
}

// cacheStore inserts or updates the cache entry for m. When inserting
// past the capacity, the least-recently-used entry is evicted in the
// same critical section.
func (c *CachedStore) cacheStore(m Mapping) {
	k := cacheKey{source: m.Source, entityType: m.EntityType, sourceID: m.SourceID}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		// Update in place and bump to front.
		el.Value.(*cacheEntry).value = m
		c.order.MoveToFront(el)
		return
	}
	entry := &cacheEntry{key: k, value: m}
	el := c.order.PushFront(entry)
	c.items[k] = el
	if c.cap > 0 && c.order.Len() > c.cap {
		c.evictLRU()
	}
}

// evictLRU drops the entry at the back of the list. Caller holds c.mu.
//
// We don't try to be clever about eviction batching — the importer
// runs Get/Put serially on each entity, so eviction happens at most
// once per write, and the constant-factor cost is tiny.
func (c *CachedStore) evictLRU() {
	el := c.order.Back()
	if el == nil {
		return
	}
	entry := el.Value.(*cacheEntry)
	delete(c.items, entry.key)
	c.order.Remove(el)
}

// Len returns the current size of the cache. Exposed for tests and
// for the importer's progress reporter.
func (c *CachedStore) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// Compile-time assertion that CachedStore implements Store. Missing
// methods would otherwise only surface at the construction site,
// possibly behind an interface assertion that obscures the type.
var _ Store = (*CachedStore)(nil)
