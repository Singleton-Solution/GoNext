package media

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// MemoryStore backs tests + the no-DB development fall-through.
type MemoryStore struct {
	mu     sync.RWMutex
	assets []Asset
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func (m *MemoryStore) Insert(a Asset) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assets = append(m.assets, a)
}

// matchesMimeClass mirrors admin/media's class predicate so the public
// surface filters the same way the admin grid does.
func matchesMimeClass(mime, class string) bool {
	switch class {
	case "":
		return true
	case "image":
		return strings.HasPrefix(mime, "image/")
	case "video":
		return strings.HasPrefix(mime, "video/")
	case "document":
		switch mime {
		case "application/pdf",
			"application/msword",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
			return true
		}
		return strings.HasPrefix(mime, "text/")
	}
	return false
}

func (m *MemoryStore) List(_ context.Context, f ListFilter) ([]Asset, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]Asset, 0, len(m.assets))
	for _, a := range m.assets {
		if !matchesMimeClass(a.MimeType, f.MimeClass) {
			continue
		}
		rows = append(rows, a)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID > rows[j].ID
	})
	if f.After != "" {
		idx := -1
		for i, a := range rows {
			marker := a.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00") + ":" + a.ID
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
	if len(rows) > limit+1 {
		rows = rows[:limit+1]
	}
	return rows, nil
}

func (m *MemoryStore) GetByID(_ context.Context, id string) (Asset, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, a := range m.assets {
		if a.ID == id {
			return a, nil
		}
	}
	return Asset{}, ErrNotFound
}
