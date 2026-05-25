package collections

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-memory Store implementation used by tests
// and by the single-binary admin smoke runner. The data structures
// are intentionally simple — a flat map plus secondary indexes — and
// the ltree semantics are emulated with string operations on the
// dot-separated path.
//
// Concurrency: protected by a single RWMutex. The hot path (List,
// Children) is read-heavy; the lock is the right level for a
// dev/test backend.
type MemoryStore struct {
	mu     sync.RWMutex
	rows   map[string]*Collection // id -> row
	clock  func() time.Time
	idGen  func() string
}

// NewMemoryStore returns an empty MemoryStore. clock is the time
// source (nil falls back to time.Now); idGen is the id generator
// (nil falls back to uuid.NewString). Tests pin both for
// deterministic output.
func NewMemoryStore(clock func() time.Time, idGen func() string) *MemoryStore {
	if clock == nil {
		clock = time.Now
	}
	if idGen == nil {
		idGen = uuid.NewString
	}
	return &MemoryStore{
		rows:  make(map[string]*Collection),
		clock: clock,
		idGen: idGen,
	}
}

// Create inserts a new collection. The path is computed from the
// parent's path + the slug; the depth check happens here, not in the
// caller, so the Store remains the single source of truth for the
// hierarchy invariants.
func (s *MemoryStore) Create(_ context.Context, in CreateInput) (Collection, error) {
	if err := validateSlug(in.Slug); err != nil {
		return Collection{}, err
	}
	if err := validateName(in.Name); err != nil {
		return Collection{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var parentPath string
	if in.ParentID != nil {
		parent, ok := s.rows[*in.ParentID]
		if !ok {
			return Collection{}, ErrNotFound
		}
		// Depth check: parent is at depth N, child would be at N+1.
		// The path counts dots; root parent has zero dots and would
		// produce a depth-1 child, which is fine.
		if parent.Depth() >= MaxDepth-1 {
			return Collection{}, ErrTooDeep
		}
		parentPath = parent.Path
	}

	// Sibling slug conflict check. Walk every row with matching
	// parent_id and check for a slug collision. O(N) per Create
	// which is fine for tests; the Postgres backend will rely on
	// the partial unique index for the same check at constant cost.
	for _, row := range s.rows {
		if !ptrEqual(row.ParentID, in.ParentID) {
			continue
		}
		if strings.EqualFold(row.Slug, in.Slug) {
			return Collection{}, ErrSlugConflict
		}
	}

	now := s.clock().UTC()
	id := s.idGen()
	path := in.Slug
	if parentPath != "" {
		path = parentPath + "." + in.Slug
	}
	row := &Collection{
		ID:        id,
		Slug:      in.Slug,
		Name:      strings.TrimSpace(in.Name),
		Path:      path,
		ParentID:  copyStringPtr(in.ParentID),
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.rows[id] = row
	return *row, nil
}

// GetByID returns the row, or ErrNotFound.
func (s *MemoryStore) GetByID(_ context.Context, id string) (Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.rows[id]
	if !ok {
		return Collection{}, ErrNotFound
	}
	return *row, nil
}

// GetByPath returns the row matching the exact ltree path. The
// admin route handler uses this to resolve /collections/marketing/2026
// — it joins the path segments with "." and asks the store.
func (s *MemoryStore) GetByPath(_ context.Context, path string) (Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, row := range s.rows {
		if row.Path == path {
			return *row, nil
		}
	}
	return Collection{}, ErrNotFound
}

// List returns every collection sorted by path (depth-first
// pre-order). The lexical sort of the dotted path lines up exactly
// with the depth-first order because of the slug character set
// constraint.
func (s *MemoryStore) List(_ context.Context) ([]Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Collection, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Children returns the direct children of parentID, ordered by name.
func (s *MemoryStore) Children(_ context.Context, parentID *string) ([]Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Collection, 0)
	for _, row := range s.rows {
		if ptrEqual(row.ParentID, parentID) {
			out = append(out, *row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Descendants returns every collection whose path is at or below
// the given path (inclusive). Mirrors the ltree `<@` operator.
func (s *MemoryStore) Descendants(_ context.Context, path string) ([]Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Collection, 0)
	for _, row := range s.rows {
		if path == "" || row.Path == path || strings.HasPrefix(row.Path, path+".") {
			out = append(out, *row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Rename mutates a collection's slug and/or name. A slug change
// rewrites the path for the row and every descendant; a name-only
// change is a one-row update.
func (s *MemoryStore) Rename(_ context.Context, id string, in UpdateInput) (Collection, error) {
	if in.Slug == nil && in.Name == nil {
		return Collection{}, nil
	}
	if in.Slug != nil {
		if err := validateSlug(*in.Slug); err != nil {
			return Collection{}, err
		}
	}
	if in.Name != nil {
		if err := validateName(*in.Name); err != nil {
			return Collection{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	row, ok := s.rows[id]
	if !ok {
		return Collection{}, ErrNotFound
	}

	oldPath := row.Path
	newSlug := row.Slug
	if in.Slug != nil && *in.Slug != row.Slug {
		newSlug = *in.Slug
		// Sibling slug conflict check at the existing parent.
		for _, other := range s.rows {
			if other.ID == row.ID {
				continue
			}
			if !ptrEqual(other.ParentID, row.ParentID) {
				continue
			}
			if strings.EqualFold(other.Slug, newSlug) {
				return Collection{}, ErrSlugConflict
			}
		}
	}

	now := s.clock().UTC()

	// Compute the new path. If the slug didn't change, the path is
	// unchanged. Otherwise, the leaf segment of the path is
	// replaced.
	newPath := oldPath
	if newSlug != row.Slug {
		// Replace the trailing slug segment with the new slug.
		if strings.Contains(oldPath, ".") {
			parentPath := oldPath[:strings.LastIndex(oldPath, ".")]
			newPath = parentPath + "." + newSlug
		} else {
			newPath = newSlug
		}
	}

	// Apply the rewrite. If the path changed, every descendant's
	// path must be updated with the new prefix. We do this in
	// place, holding the lock for the duration.
	if newPath != oldPath {
		for _, other := range s.rows {
			if other.Path == oldPath || strings.HasPrefix(other.Path, oldPath+".") {
				other.Path = newPath + other.Path[len(oldPath):]
				other.UpdatedAt = now
			}
		}
	}
	row.Slug = newSlug
	if in.Name != nil {
		row.Name = strings.TrimSpace(*in.Name)
	}
	row.UpdatedAt = now
	return *row, nil
}

// Move re-parents a collection. The path of the collection and
// every descendant is rewritten.
func (s *MemoryStore) Move(_ context.Context, id string, in MoveInput) (Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, ok := s.rows[id]
	if !ok {
		return Collection{}, ErrNotFound
	}

	// Cycle check: the new parent can't be the row itself or any of
	// its descendants. We compute the descendant set first, then
	// look up the candidate parent.
	if in.NewParentID != nil {
		if *in.NewParentID == row.ID {
			return Collection{}, ErrCycle
		}
		descendants := make(map[string]struct{})
		for _, other := range s.rows {
			if other.Path == row.Path || strings.HasPrefix(other.Path, row.Path+".") {
				descendants[other.ID] = struct{}{}
			}
		}
		if _, isDescendant := descendants[*in.NewParentID]; isDescendant {
			return Collection{}, ErrCycle
		}
	}

	// Resolve the new parent path.
	var newParentPath string
	var newParentDepth int
	if in.NewParentID != nil {
		parent, ok := s.rows[*in.NewParentID]
		if !ok {
			return Collection{}, ErrNotFound
		}
		newParentPath = parent.Path
		newParentDepth = parent.Depth() + 1
	}

	// Depth check: the deepest descendant after the move must not
	// exceed MaxDepth-1.
	oldPath := row.Path
	oldDepth := row.Depth()
	for _, other := range s.rows {
		if other.Path == oldPath || strings.HasPrefix(other.Path, oldPath+".") {
			delta := other.Depth() - oldDepth
			if newParentDepth+delta > MaxDepth-1 {
				return Collection{}, ErrTooDeep
			}
		}
	}

	// Sibling slug conflict check at the new parent.
	for _, other := range s.rows {
		if other.ID == row.ID {
			continue
		}
		if !ptrEqual(other.ParentID, in.NewParentID) {
			continue
		}
		if strings.EqualFold(other.Slug, row.Slug) {
			return Collection{}, ErrSlugConflict
		}
	}

	// Compute the new path for the row.
	newPath := row.Slug
	if newParentPath != "" {
		newPath = newParentPath + "." + row.Slug
	}

	now := s.clock().UTC()

	// Rewrite every descendant first (uses oldPath prefix), then
	// the row itself.
	for _, other := range s.rows {
		if other.ID == row.ID {
			continue
		}
		if other.Path == oldPath || strings.HasPrefix(other.Path, oldPath+".") {
			other.Path = newPath + other.Path[len(oldPath):]
			other.UpdatedAt = now
		}
	}
	row.Path = newPath
	row.ParentID = copyStringPtr(in.NewParentID)
	row.UpdatedAt = now
	return *row, nil
}

// Delete removes a collection and every descendant. Mirrors the
// ON DELETE CASCADE that the FK has.
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, ok := s.rows[id]
	if !ok {
		return ErrNotFound
	}
	prefix := row.Path
	toDelete := make([]string, 0)
	for otherID, other := range s.rows {
		if other.Path == prefix || strings.HasPrefix(other.Path, prefix+".") {
			toDelete = append(toDelete, otherID)
		}
	}
	for _, k := range toDelete {
		delete(s.rows, k)
	}
	return nil
}

// ptrEqual reports whether two *string values point to equal strings
// (both nil, or both non-nil and equal). The parent_id comparator
// uses this so a nil-vs-nil root match works without ceremony at
// each call site.
func ptrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// copyStringPtr defensively copies a *string so a caller mutating
// their copy doesn't poison the stored row.
func copyStringPtr(p *string) *string {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}
