package media

import (
	"sync"
	"sync/atomic"
)

// MemoryCounter is an in-memory Counter implementation. It exists for
// two reasons:
//
//  1. Tests in this package (and downstream callers) need a Counter
//     they can assert on without standing up Prometheus.
//
//  2. Small embedded callers that ship without a metrics backend at
//     all can still wire a Counter and read its values via Snapshot.
//
// Production HTTP services should NOT use MemoryCounter — wire a
// Prometheus adapter instead, so the counters appear in the standard
// /metrics scrape.
//
// MemoryCounter is safe for concurrent use. Internally it keeps a
// sync.Map of *atomic.Int64 so the hot path (Inc on an existing key)
// is lock-free.
type MemoryCounter struct {
	mu      sync.Mutex // guards creation of new entries in m
	entries sync.Map   // name string -> *atomic.Int64
}

// NewMemoryCounter returns a zeroed MemoryCounter ready for use.
func NewMemoryCounter() *MemoryCounter {
	return &MemoryCounter{}
}

// Inc atomically increments the counter named name by one. Unknown
// names are created on first sight. Safe for concurrent use.
func (c *MemoryCounter) Inc(name string) {
	if v, ok := c.entries.Load(name); ok {
		v.(*atomic.Int64).Add(1)
		return
	}
	// First touch: take the lock to avoid two goroutines each creating
	// a separate Int64 for the same name (which would lose increments).
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.entries.Load(name); ok {
		v.(*atomic.Int64).Add(1)
		return
	}
	var n atomic.Int64
	n.Store(1)
	c.entries.Store(name, &n)
}

// Get returns the current value of counter name, or 0 if it has never
// been incremented. Reads are atomic with respect to concurrent Inc.
func (c *MemoryCounter) Get(name string) int64 {
	v, ok := c.entries.Load(name)
	if !ok {
		return 0
	}
	return v.(*atomic.Int64).Load()
}

// Snapshot returns a map copy of all counter values at the moment of
// the call. Useful for test assertions; callers can mutate the result
// without affecting the underlying counter.
func (c *MemoryCounter) Snapshot() map[string]int64 {
	out := make(map[string]int64)
	c.entries.Range(func(k, v any) bool {
		out[k.(string)] = v.(*atomic.Int64).Load()
		return true
	})
	return out
}
