package reusable

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-process Store implementation. Backs unit
// tests and the no-DB development fall-through.
//
// Concurrency: one sync.RWMutex guards every read and write. The
// admin surface is a low-frequency operator path, so the simplest
// correct strategy wins — the same call shape used by every other
// admin-side in-memory store in the codebase.
type MemoryStore struct {
	mu sync.RWMutex

	// rows is keyed by entry ID.
	rows map[uuid.UUID]Entry

	// now returns the wall clock. Tests inject a deterministic clock;
	// production wiring passes time.Now.
	now func() time.Time
}

// NewMemoryStore returns an empty MemoryStore using time.Now as its
// clock. Tests use NewMemoryStoreWithClock to pin the time source.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(time.Now)
}

// NewMemoryStoreWithClock returns an empty MemoryStore using the
// supplied clock. nil falls back to time.Now.
func NewMemoryStoreWithClock(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		rows: make(map[uuid.UUID]Entry),
		now:  now,
	}
}

// Create persists a new entry, assigning an ID and stamping timestamps.
func (s *MemoryStore) Create(_ context.Context, e Entry) (Entry, error) {
	if err := validate(e); err != nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrInvalidEntry, err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	now := s.now()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.UpdatedAt = now
	// Default the JSON fields if the caller passed nil — the table's
	// DEFAULT clause does the same for the Postgres path.
	if len(e.Attrs) == 0 {
		e.Attrs = json.RawMessage(`{}`)
	}
	if len(e.Content) == 0 {
		e.Content = json.RawMessage(`[]`)
	}
	s.rows[e.ID] = e
	return e, nil
}

// Get returns a single entry by ID, or ErrNotFound.
func (s *MemoryStore) Get(_ context.Context, id uuid.UUID) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.rows[id]
	if !ok {
		return Entry{}, fmt.Errorf("%w: id=%s", ErrNotFound, id)
	}
	return e, nil
}

// Update mutates the editable fields of an existing entry.
func (s *MemoryStore) Update(_ context.Context, e Entry) (Entry, error) {
	if err := validate(e); err != nil {
		return Entry{}, fmt.Errorf("%w: %s", ErrInvalidEntry, err.Error())
	}
	if e.ID == uuid.Nil {
		return Entry{}, fmt.Errorf("%w: zero ID", ErrInvalidEntry)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.rows[e.ID]
	if !ok {
		return Entry{}, fmt.Errorf("%w: id=%s", ErrNotFound, e.ID)
	}
	existing.Name = e.Name
	if len(e.Attrs) > 0 {
		existing.Attrs = e.Attrs
	}
	if len(e.Content) > 0 {
		existing.Content = e.Content
	}
	existing.UpdatedAt = s.now()
	s.rows[e.ID] = existing
	return existing, nil
}

// Delete removes the entry with the given ID. Idempotent: deleting an
// unknown ID returns nil.
func (s *MemoryStore) Delete(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

// List returns matching entries sorted by created_at DESC.
func (s *MemoryStore) List(_ context.Context, f ListFilter) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.rows))
	for _, e := range s.rows {
		if f.NameContains != "" && !containsFold(e.Name, f.NameContains) {
			continue
		}
		if !f.Before.IsZero() && !e.CreatedAt.Before(f.Before) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID.String() < out[j].ID.String()
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetMany fetches every entry whose ID is in ids.
func (s *MemoryStore) GetMany(_ context.Context, ids []uuid.UUID) ([]Entry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		if e, ok := s.rows[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// containsFold is a tiny case-insensitive substring check. We could
// pull in strings.Contains + strings.ToLower, but the inline form
// avoids the allocation and matches the in-memory filter shape used
// in admin/comments.
func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	// Case-folded comparison without allocating new strings:
	// scan and compare letter-by-letter.
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
