package customfields

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-memory Store backing tests + the no-DB
// development fall-through. Concurrency: one mutex for everything;
// the workloads this store supports are small enough that fine-
// grained locking buys no measurable headroom.
type MemoryStore struct {
	mu     sync.RWMutex
	groups map[string]FieldGroup            // by id
	meta   map[string]map[string]MetaValue  // post_id -> group_id -> value
	now    func() time.Time
}

// NewMemoryStore returns an empty store using time.Now for
// timestamps.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(time.Now)
}

// NewMemoryStoreWithClock returns an empty store using the supplied
// clock. nil falls back to time.Now.
func NewMemoryStoreWithClock(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		groups: make(map[string]FieldGroup),
		meta:   make(map[string]map[string]MetaValue),
		now:    now,
	}
}

func (s *MemoryStore) ListGroups(_ context.Context) ([]FieldGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]FieldGroup, 0, len(s.groups))
	for _, g := range s.groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (s *MemoryStore) GetGroup(_ context.Context, id string) (FieldGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.groups[id]
	if !ok {
		return FieldGroup{}, ErrNotFound
	}
	return g, nil
}

func (s *MemoryStore) GetGroupBySlug(_ context.Context, slug string) (FieldGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	want := strings.ToLower(slug)
	for _, g := range s.groups {
		if strings.ToLower(g.Slug) == want {
			return g, nil
		}
	}
	return FieldGroup{}, ErrNotFound
}

func (s *MemoryStore) InsertGroup(_ context.Context, in FieldGroupCreate) (FieldGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.groups {
		if strings.EqualFold(g.Slug, in.Slug) {
			return FieldGroup{}, ErrDuplicateSlug
		}
	}
	now := s.now()
	g := FieldGroup{
		ID:        uuid.New().String(),
		Slug:      in.Slug,
		Title:     in.Title,
		PostTypes: append([]string(nil), in.PostTypes...),
		Schema:    json.RawMessage(append([]byte(nil), in.Schema...)),
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
	}
	s.groups[g.ID] = g
	return g, nil
}

func (s *MemoryStore) UpdateGroup(_ context.Context, id string, version int, u FieldGroupUpdate) (FieldGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[id]
	if !ok {
		return FieldGroup{}, ErrNotFound
	}
	if g.Version != version {
		return FieldGroup{}, ErrVersionConflict
	}
	if u.Title != nil {
		g.Title = *u.Title
	}
	if u.PostTypes != nil {
		g.PostTypes = append([]string(nil), (*u.PostTypes)...)
	}
	if u.Schema != nil {
		g.Schema = json.RawMessage(append([]byte(nil), (*u.Schema)...))
	}
	g.UpdatedAt = s.now()
	g.Version++
	s.groups[id] = g
	return g, nil
}

func (s *MemoryStore) DeleteGroup(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.groups[id]; !ok {
		return ErrNotFound
	}
	delete(s.groups, id)
	// Cascade: drop every meta value attached to this group.
	for postID, byGroup := range s.meta {
		delete(byGroup, id)
		if len(byGroup) == 0 {
			delete(s.meta, postID)
		}
	}
	return nil
}

func (s *MemoryStore) ListMeta(_ context.Context, postID string) ([]MetaValue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byGroup, ok := s.meta[postID]
	if !ok {
		return nil, nil
	}
	out := make([]MetaValue, 0, len(byGroup))
	for _, v := range byGroup {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GroupID < out[j].GroupID })
	return out, nil
}

func (s *MemoryStore) GetMeta(_ context.Context, postID, groupID string) (MetaValue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byGroup, ok := s.meta[postID]
	if !ok {
		return MetaValue{}, ErrNotFound
	}
	v, ok := byGroup[groupID]
	if !ok {
		return MetaValue{}, ErrNotFound
	}
	return v, nil
}

func (s *MemoryStore) PutMeta(_ context.Context, postID, groupID string, values json.RawMessage) (MetaValue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.groups[groupID]; !ok {
		return MetaValue{}, ErrNotFound
	}
	if _, ok := s.meta[postID]; !ok {
		s.meta[postID] = make(map[string]MetaValue)
	}
	mv := MetaValue{
		PostID:    postID,
		GroupID:   groupID,
		Values:    json.RawMessage(append([]byte(nil), values...)),
		UpdatedAt: s.now(),
	}
	s.meta[postID][groupID] = mv
	return mv, nil
}
