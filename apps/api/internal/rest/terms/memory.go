package terms

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// MemoryStore backs tests and the no-DB development fall-through.
type MemoryStore struct {
	mu         sync.RWMutex
	taxonomies []Taxonomy
	terms      []Term
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func (m *MemoryStore) AddTaxonomy(t Taxonomy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.taxonomies = append(m.taxonomies, t)
}

func (m *MemoryStore) AddTerm(t Term) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terms = append(m.terms, t)
}

func (m *MemoryStore) ListTaxonomies(_ context.Context) ([]Taxonomy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Taxonomy, len(m.taxonomies))
	copy(out, m.taxonomies)
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (m *MemoryStore) GetTaxonomy(_ context.Context, slug string) (Taxonomy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.taxonomies {
		if strings.EqualFold(t.Slug, slug) {
			return t, nil
		}
	}
	return Taxonomy{}, ErrNotFound
}

func (m *MemoryStore) ListTerms(_ context.Context, f TermListFilter) ([]Term, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]Term, 0, len(m.terms))
	search := strings.ToLower(f.Search)
	for _, t := range m.terms {
		if f.Taxonomy != "" && t.Taxonomy != f.Taxonomy {
			continue
		}
		if f.ParentPresent {
			parent := ""
			if t.ParentID != nil {
				parent = *t.ParentID
			}
			if parent != f.ParentID {
				continue
			}
		}
		if search != "" && !strings.Contains(strings.ToLower(t.Name), search) {
			continue
		}
		rows = append(rows, t)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Path != rows[j].Path {
			return rows[i].Path < rows[j].Path
		}
		return rows[i].ID < rows[j].ID
	})
	if f.After != "" {
		idx := -1
		for i, t := range rows {
			if t.Path+":"+t.ID == f.After {
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

func (m *MemoryStore) GetTerm(_ context.Context, id string) (Term, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.terms {
		if t.ID == id {
			return t, nil
		}
	}
	return Term{}, ErrNotFound
}
