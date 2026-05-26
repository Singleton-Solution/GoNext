package audit

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// memoryDefaultLimit is the default Filter.Limit applied when the caller
// leaves it zero. The cap exists so a forgotten filter doesn't dump the
// entire in-memory log on a test that's already pushed thousands of events.
const memoryDefaultLimit = 100

// MemoryStore is an in-process Store backed by a slice. It's designed
// for unit tests and short-lived development scenarios — there is no
// persistence and no eviction, so a long-running process will leak.
//
// Concurrency: safe for concurrent Emit and List. Internally guarded
// by a single sync.RWMutex; contention is acceptable at test scale.
//
// Time injection: NowFunc lets tests pin Event.Time without sleeping.
// If left nil, time.Now is used.
type MemoryStore struct {
	mu     sync.RWMutex
	events []Event
	nextID atomic.Int64

	// NowFunc, if set, replaces time.Now for Event.Time defaults. Tests
	// pin it to a fixed instant to make ordering deterministic.
	NowFunc func() time.Time
}

// NewMemoryStore returns an empty in-memory store ready for use.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// MostRecent returns the most-recently-emitted event, or a zero
// Event if the store is empty. Used as a PrevFetcher for the audit
// chain — callers wire it into ChainConfig.PrevFetcher.
//
// The returned event is a copy; the caller may mutate it without
// affecting the store.
func (s *MemoryStore) MostRecent() (Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) == 0 {
		return Event{}, nil
	}
	return s.events[len(s.events)-1], nil
}

func (s *MemoryStore) now() time.Time {
	if s.NowFunc != nil {
		return s.NowFunc()
	}
	return time.Now()
}

// Emit appends e to the store. Returns ErrInvalidEvent (wrapped) if e
// is structurally invalid. The store assigns Event.ID if the caller
// left it empty.
func (s *MemoryStore) Emit(_ context.Context, e Event) error {
	if err := validateForEmit(e); err != nil {
		return err
	}
	normalized := e.normalize(s.now)
	if normalized.ID == "" {
		normalized.ID = fmt.Sprintf("mem-%d", s.nextID.Add(1))
	}

	s.mu.Lock()
	s.events = append(s.events, normalized)
	s.mu.Unlock()
	return nil
}

// List returns events matching f, most recent first.
func (s *MemoryStore) List(_ context.Context, f Filter) ([]Event, error) {
	s.mu.RLock()
	// Snapshot under the lock so we can iterate without holding it.
	snapshot := make([]Event, len(s.events))
	copy(snapshot, s.events)
	s.mu.RUnlock()

	filtered := snapshot[:0]
	for _, e := range snapshot {
		if !matchFilter(e, f) {
			continue
		}
		filtered = append(filtered, e)
	}

	// Sort by Time DESC, with ID as a deterministic tiebreaker. The
	// tiebreaker matters because tests often inject identical
	// timestamps via NowFunc; without it, sort.Slice's stability is
	// undefined for cross-Go-version reproducibility.
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Time.Equal(filtered[j].Time) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].Time.After(filtered[j].Time)
	})

	limit := f.Limit
	if limit <= 0 {
		limit = memoryDefaultLimit
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

// matchFilter reports whether e satisfies every non-zero field of f.
//
// Empty filter fields are wildcards. The PluginSlug field has a subtle
// rule: an empty filter matches everything (including plugin events),
// because that's the principle-of-least-surprise default.
func matchFilter(e Event, f Filter) bool {
	if !f.Start.IsZero() && e.Time.Before(f.Start) {
		return false
	}
	if !f.End.IsZero() && e.Time.After(f.End) {
		return false
	}
	if f.ActorUserID != "" && e.ActorUserID != f.ActorUserID {
		return false
	}
	if f.PluginSlug != "" && e.ActorPluginSlug != f.PluginSlug {
		return false
	}
	if f.EventType != "" && e.EventType != f.EventType {
		return false
	}
	if f.Severity != "" && e.Severity != f.Severity {
		return false
	}
	return true
}
