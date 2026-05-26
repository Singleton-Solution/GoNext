package menus

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the in-process Store used by tests and the no-DB
// development fallthrough. Goroutine-safe.
type MemoryStore struct {
	mu    sync.RWMutex
	menus map[uuid.UUID]Menu
	// items keyed by menu_id then by item_id for O(1) lookup during
	// reorder transactions.
	items map[uuid.UUID]map[uuid.UUID]MenuItem
	// now is the clock source — overridable in tests.
	now func() time.Time
}

// NewMemoryStore builds an empty in-memory Store ready for use.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		menus: make(map[uuid.UUID]Menu),
		items: make(map[uuid.UUID]map[uuid.UUID]MenuItem),
		now:   time.Now,
	}
}

// CreateMenu implements [Store.CreateMenu].
func (s *MemoryStore) CreateMenu(_ context.Context, m Menu) (Menu, error) {
	if err := validateMenu(m); err != nil {
		return Menu{}, fmt.Errorf("%w: %s", ErrInvalidMenu, err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Slug must be unique.
	for _, existing := range s.menus {
		if existing.Slug == m.Slug {
			return Menu{}, fmt.Errorf("%w: slug already exists", ErrInvalidMenu)
		}
	}
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	now := s.now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	if len(m.Attrs) == 0 {
		m.Attrs = json.RawMessage(`{}`)
	}
	s.menus[m.ID] = m
	s.items[m.ID] = make(map[uuid.UUID]MenuItem)
	return m, nil
}

// GetMenu implements [Store.GetMenu].
func (s *MemoryStore) GetMenu(_ context.Context, id uuid.UUID) (Menu, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.menus[id]
	if !ok {
		return Menu{}, fmt.Errorf("%w: id=%s", ErrNotFound, id)
	}
	return m, nil
}

// GetMenuBySlug implements [Store.GetMenuBySlug].
func (s *MemoryStore) GetMenuBySlug(_ context.Context, slug string) (Menu, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.menus {
		if m.Slug == slug {
			return m, nil
		}
	}
	return Menu{}, fmt.Errorf("%w: slug=%s", ErrNotFound, slug)
}

// UpdateMenu implements [Store.UpdateMenu]. Slug is immutable; the
// stored slug overrides any value the caller supplies.
func (s *MemoryStore) UpdateMenu(_ context.Context, m Menu) (Menu, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.menus[m.ID]
	if !ok {
		return Menu{}, fmt.Errorf("%w: id=%s", ErrNotFound, m.ID)
	}
	// Pin slug to existing — the contract is "slug is immutable".
	m.Slug = existing.Slug
	m.CreatedAt = existing.CreatedAt
	if err := validateMenu(m); err != nil {
		return Menu{}, fmt.Errorf("%w: %s", ErrInvalidMenu, err.Error())
	}
	m.UpdatedAt = s.now().UTC()
	if len(m.Attrs) == 0 {
		m.Attrs = json.RawMessage(`{}`)
	}
	s.menus[m.ID] = m
	return m, nil
}

// DeleteMenu implements [Store.DeleteMenu]. Cascades to items.
func (s *MemoryStore) DeleteMenu(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.menus, id)
	delete(s.items, id)
	return nil
}

// ListMenus implements [Store.ListMenus]. Returned slice is sorted by
// name ascending.
func (s *MemoryStore) ListMenus(_ context.Context) ([]Menu, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Menu, 0, len(s.menus))
	for _, m := range s.menus {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CreateItem implements [Store.CreateItem].
func (s *MemoryStore) CreateItem(_ context.Context, mi MenuItem) (MenuItem, error) {
	if err := validateItem(mi); err != nil {
		return MenuItem{}, fmt.Errorf("%w: %s", ErrInvalidItem, err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	menuItems, ok := s.items[mi.MenuID]
	if !ok {
		return MenuItem{}, fmt.Errorf("%w: menu_id=%s", ErrNotFound, mi.MenuID)
	}
	// Path uniqueness within menu.
	for _, existing := range menuItems {
		if existing.Path == mi.Path {
			return MenuItem{}, fmt.Errorf("%w: path %s already in use", ErrInvalidItem, mi.Path)
		}
	}
	if mi.ID == uuid.Nil {
		mi.ID = uuid.New()
	}
	now := s.now().UTC()
	mi.CreatedAt = now
	mi.UpdatedAt = now
	if len(mi.Attrs) == 0 {
		mi.Attrs = json.RawMessage(`{}`)
	}
	menuItems[mi.ID] = mi
	return mi, nil
}

// UpdateItem implements [Store.UpdateItem].
func (s *MemoryStore) UpdateItem(_ context.Context, mi MenuItem) (MenuItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	menuItems, ok := s.items[mi.MenuID]
	if !ok {
		return MenuItem{}, fmt.Errorf("%w: menu_id=%s", ErrNotFound, mi.MenuID)
	}
	existing, ok := menuItems[mi.ID]
	if !ok {
		return MenuItem{}, fmt.Errorf("%w: id=%s", ErrNotFound, mi.ID)
	}
	mi.CreatedAt = existing.CreatedAt
	if err := validateItem(mi); err != nil {
		return MenuItem{}, fmt.Errorf("%w: %s", ErrInvalidItem, err.Error())
	}
	mi.UpdatedAt = s.now().UTC()
	if len(mi.Attrs) == 0 {
		mi.Attrs = json.RawMessage(`{}`)
	}
	menuItems[mi.ID] = mi
	return mi, nil
}

// DeleteItem implements [Store.DeleteItem].
func (s *MemoryStore) DeleteItem(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, menuItems := range s.items {
		if _, ok := menuItems[id]; ok {
			delete(menuItems, id)
			return nil
		}
	}
	return nil
}

// ReorderItems implements [Store.ReorderItems]. All-or-nothing under
// the store lock.
func (s *MemoryStore) ReorderItems(_ context.Context, menuID uuid.UUID, items []MenuItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	menuItems, ok := s.items[menuID]
	if !ok {
		return fmt.Errorf("%w: menu_id=%s", ErrNotFound, menuID)
	}
	// Validate every item before applying any change.
	for _, mi := range items {
		if _, ok := menuItems[mi.ID]; !ok {
			return fmt.Errorf("%w: item id=%s", ErrNotFound, mi.ID)
		}
		if err := validateItem(mi); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidItem, err.Error())
		}
	}
	now := s.now().UTC()
	for _, mi := range items {
		existing := menuItems[mi.ID]
		existing.Path = mi.Path
		existing.UpdatedAt = now
		menuItems[mi.ID] = existing
	}
	return nil
}

// GetWithItems implements [Store.GetWithItems].
func (s *MemoryStore) GetWithItems(ctx context.Context, id uuid.UUID) (MenuWithItems, error) {
	m, err := s.GetMenu(ctx, id)
	if err != nil {
		return MenuWithItems{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := MenuWithItems{Menu: m}
	for _, mi := range s.items[id] {
		out.Items = append(out.Items, mi)
	}
	sort.Slice(out.Items, func(i, j int) bool {
		return out.Items[i].Path < out.Items[j].Path
	})
	return out, nil
}

// GetWithItemsBySlug implements [Store.GetWithItemsBySlug].
func (s *MemoryStore) GetWithItemsBySlug(ctx context.Context, slug string) (MenuWithItems, error) {
	m, err := s.GetMenuBySlug(ctx, slug)
	if err != nil {
		return MenuWithItems{}, err
	}
	return s.GetWithItems(ctx, m.ID)
}
