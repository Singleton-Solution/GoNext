package users

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// MemoryStore is the in-memory backing for tests and the no-DB
// development fall-through. It implements Store with the same
// semantics as the Postgres variant: created_at DESC, id DESC for
// ties; cursor format is "RFC3339Nano:ID".
type MemoryStore struct {
	mu    sync.RWMutex
	users []User // ordered by insertion; queries sort copies
}

// NewMemoryStore returns an empty in-memory store. Seed with Insert.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: nil}
}

// Insert appends a row. Tests use this to seed the store; production
// uses a Postgres-backed Store instead.
func (m *MemoryStore) Insert(u User) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users = append(m.users, u)
}

// List returns a page matching f. The store fetches limit+1 rows so
// the handler can surface a next cursor.
func (m *MemoryStore) List(_ context.Context, f ListFilter) ([]User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]User, 0, len(m.users))
	prefix := strings.ToLower(f.HandlePrefix)
	for _, u := range m.users {
		if prefix != "" && !strings.HasPrefix(strings.ToLower(u.Handle), prefix) {
			continue
		}
		rows = append(rows, u)
	}
	// created_at DESC, id DESC.
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID > rows[j].ID
	})
	// Cursor: skip until we pass the after-marker.
	if f.After != "" {
		idx := -1
		for i, u := range rows {
			marker := u.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00") + ":" + u.ID
			if marker == f.After {
				idx = i
				break
			}
		}
		if idx >= 0 {
			rows = rows[idx+1:]
		}
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	// Return limit+1 to signal a next page.
	if len(rows) > limit+1 {
		rows = rows[:limit+1]
	}
	return rows, nil
}

func (m *MemoryStore) GetByID(_ context.Context, id string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *MemoryStore) GetByHandle(_ context.Context, handle string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	want := strings.ToLower(handle)
	for _, u := range m.users {
		if strings.ToLower(u.Handle) == want {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}
