package lifecycle

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStorage is an in-process Storage backed by a map.
//
// It is the canonical Storage for unit tests and short-lived dev setups.
// There is no persistence and no eviction: a long-running process will
// retain every row that ever existed for the lifetime of the
// MemoryStorage instance, which is fine for tests and unsuitable for
// production.
//
// Concurrency: every public method takes a single sync.RWMutex. The
// CAS semantics of UpdateState are enforced inside the write lock so a
// concurrent UpdateState observing the same expectedFrom cannot win.
// Contention is acceptable at test scale.
//
// Time injection: NowFunc lets tests pin Plugin.InstalledAt /
// UpdatedAt / ActivatedAt without sleeping. If left nil, time.Now is
// used.
type MemoryStorage struct {
	mu   sync.RWMutex
	rows map[string]Plugin

	// NowFunc, if set, replaces time.Now for the timestamps Storage
	// assigns on Insert and UpdateState. Tests pin it to a fixed
	// instant; production code leaves it nil.
	NowFunc func() time.Time
}

// NewMemoryStorage returns an empty in-memory Storage ready for use.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{rows: make(map[string]Plugin)}
}

func (s *MemoryStorage) now() time.Time {
	if s.NowFunc != nil {
		return s.NowFunc()
	}
	return time.Now()
}

// Insert persists p. Returns ErrAlreadyExists if a row with the same
// slug already exists. Zero-valued timestamps and RowVersion are
// populated by the implementation.
func (s *MemoryStorage) Insert(_ context.Context, p Plugin) error {
	if p.Slug == "" {
		return fmt.Errorf("lifecycle/memory: Insert: slug is required")
	}
	if !p.State.Valid() {
		return fmt.Errorf("lifecycle/memory: Insert: invalid state %q", p.State)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.rows[p.Slug]; ok {
		return fmt.Errorf("%w: %q", ErrAlreadyExists, p.Slug)
	}

	now := s.now().UTC()
	if p.InstalledAt.IsZero() {
		p.InstalledAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	if p.RowVersion == 0 {
		p.RowVersion = 1
	}
	// Capabilities is stored by-value; defensively copy so external
	// mutation of the caller's slice can't bleed into our state.
	p.Capabilities = copyStrings(p.Capabilities)
	// Manifest is a json.RawMessage — bytes — same defensive copy.
	if len(p.Manifest) > 0 {
		manifestCopy := make([]byte, len(p.Manifest))
		copy(manifestCopy, p.Manifest)
		p.Manifest = manifestCopy
	}

	s.rows[p.Slug] = p
	return nil
}

// Get returns the row identified by slug, or ErrNotFound. The returned
// Plugin is a copy — callers cannot mutate storage by retaining the
// value.
func (s *MemoryStorage) Get(_ context.Context, slug string) (Plugin, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.rows[slug]
	if !ok {
		return Plugin{}, fmt.Errorf("%w: %q", ErrNotFound, slug)
	}
	return cloneForReturn(p), nil
}

// List returns every row sorted by slug.
func (s *MemoryStorage) List(_ context.Context) ([]Plugin, error) {
	s.mu.RLock()
	out := make([]Plugin, 0, len(s.rows))
	for _, p := range s.rows {
		out = append(out, cloneForReturn(p))
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// UpdateState applies the CAS. The mutex held across the read-and-write
// makes this trivially race-free for the memory backend; the Postgres
// implementation does the same thing with a conditional UPDATE.
func (s *MemoryStorage) UpdateState(_ context.Context, slug string, expectedFrom, newState State, fields *StateUpdateFields) error {
	if !newState.Valid() {
		return fmt.Errorf("lifecycle/memory: UpdateState: invalid newState %q", newState)
	}
	if !expectedFrom.Valid() {
		return fmt.Errorf("lifecycle/memory: UpdateState: invalid expectedFrom %q", expectedFrom)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.rows[slug]
	if !ok {
		// Match the error shape of UpdateState-when-not-there in the
		// Postgres path: the row not existing AND the row existing in
		// a different state are both lost-race conditions from the
		// Manager's perspective. We return ErrInvalidTransition so the
		// Manager doesn't have to special-case "deleted under us".
		return transitionError(slug, "UpdateState", expectedFrom, "")
	}
	if p.State != expectedFrom {
		return transitionError(slug, "UpdateState", expectedFrom, p.State)
	}

	p.State = newState
	p.RowVersion++
	p.UpdatedAt = s.now().UTC()
	if fields != nil {
		if fields.ActivatedAt != nil {
			p.ActivatedAt = (*fields.ActivatedAt).UTC()
		}
		if fields.LastError != nil {
			p.LastError = *fields.LastError
		}
		if fields.ErrorAt != nil {
			p.ErrorAt = (*fields.ErrorAt).UTC()
		}
	}

	s.rows[slug] = p
	return nil
}

// Delete removes the row identified by slug. Returns ErrNotFound if no
// such row exists.
func (s *MemoryStorage) Delete(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.rows[slug]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, slug)
	}
	delete(s.rows, slug)
	return nil
}

// cloneForReturn deep-copies the slice/map fields of p so a caller
// holding the returned value cannot mutate Storage by mutating those
// references.
func cloneForReturn(p Plugin) Plugin {
	out := p
	out.Capabilities = copyStrings(p.Capabilities)
	if len(p.Manifest) > 0 {
		manifestCopy := make([]byte, len(p.Manifest))
		copy(manifestCopy, p.Manifest)
		out.Manifest = manifestCopy
	}
	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// Ensure MemoryStorage satisfies the Storage interface at compile time.
var _ Storage = (*MemoryStorage)(nil)
