package oauth

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryStateStore is an in-process StateStore backed by a map and a
// mutex. It is intended for:
//
//   - single-binary deployments where horizontal scale-out is not yet a
//     concern (a Pi-on-the-desk install, a dev laptop, a CI runner);
//   - unit and integration tests that need a StateStore without
//     pulling Redis into the test matrix.
//
// Production multi-replica deployments should use a Redis-backed
// StateStore (lands in a follow-up issue) so a state issued by replica A
// is consumable by replica B when the IdP callback lands on a different
// pod than the original /authorize request.
//
// MemoryStateStore is safe for concurrent use. Expired entries are
// evicted lazily on Get; an optional background sweeper can be enabled
// via NewMemoryStateStoreWithSweep for long-running processes that want
// to bound peak memory usage when many flows time out without callback.
type MemoryStateStore struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	// now is the time source. nil means time.Now; tests can pin it for
	// deterministic TTL behaviour without sleeping.
	now func() time.Time
}

type memoryEntry struct {
	data      StateData
	expiresAt time.Time
}

// NewMemoryStateStore returns a fresh in-process StateStore. No
// background goroutines are started; eviction is lazy. For a sweeper
// variant see NewMemoryStateStoreWithSweep.
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{
		entries: make(map[string]memoryEntry),
	}
}

// newMemoryStateStoreWithClock is an internal constructor used by tests
// to pin the time source. Production callers should use
// NewMemoryStateStore.
func newMemoryStateStoreWithClock(now func() time.Time) *MemoryStateStore {
	return &MemoryStateStore{
		entries: make(map[string]memoryEntry),
		now:     now,
	}
}

// nowFunc returns the configured clock, defaulting to time.Now. We
// resolve the function on every call rather than baking it in at
// construction because the test clock may itself be a closure over a
// counter that the test mutates between operations.
func (s *MemoryStateStore) nowFunc() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Put records data under state. Returns ErrEmptyState if state is empty.
// The ctx parameter is accepted for interface compliance but is not
// consulted — in-memory store operations are non-blocking.
//
// A non-positive TTL is treated as "expired immediately", which makes
// Put followed by Get always fail with ErrStateNotFound. That is a
// deliberate choice over silently substituting DefaultStateTTL: a caller
// that passes ttl=0 has almost certainly miscomputed the value, and
// fail-closed is the right discipline for an auth primitive.
func (s *MemoryStateStore) Put(_ context.Context, state string, data StateData, ttl time.Duration) error {
	if state == "" {
		return ErrEmptyState
	}
	now := s.nowFunc()
	if data.CreatedAt.IsZero() {
		data.CreatedAt = now
	}
	s.mu.Lock()
	s.entries[state] = memoryEntry{
		data:      data,
		expiresAt: now.Add(ttl),
	}
	s.mu.Unlock()
	return nil
}

// Get returns the data for state and atomically removes the entry.
// Returns ErrStateNotFound if state is unknown, expired, or already
// consumed. ctx is accepted for interface compliance.
//
// Expired entries are evicted as a side effect: if Get finds an entry
// past its expiry it deletes it and returns ErrStateNotFound. This means
// a busy install does not need the optional sweeper at all — entries
// turn over with the call rate.
func (s *MemoryStateStore) Get(_ context.Context, state string) (StateData, error) {
	if state == "" {
		return StateData{}, ErrEmptyState
	}
	now := s.nowFunc()

	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[state]
	if !ok {
		return StateData{}, fmt.Errorf("%w: state unknown", ErrStateNotFound)
	}
	// Always delete on Get — even if expired — so a second Get fails
	// closed regardless of timing. This is the single-use property.
	delete(s.entries, state)
	if !e.expiresAt.After(now) {
		return StateData{}, fmt.Errorf("%w: state expired", ErrStateNotFound)
	}
	return e.data, nil
}

// Len returns the number of live (not necessarily unexpired) entries.
// Exposed for tests and admin diagnostics; not part of the StateStore
// interface.
func (s *MemoryStateStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Sweep removes all expired entries from the store. It is exposed for
// callers that want to bound peak memory: a long-running process where
// many flows time out without callback can call Sweep on a ticker.
//
// Returns the number of entries removed.
func (s *MemoryStateStore) Sweep() int {
	now := s.nowFunc()
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for k, e := range s.entries {
		if !e.expiresAt.After(now) {
			delete(s.entries, k)
			removed++
		}
	}
	return removed
}
