package site_editor

import (
	"context"
	"fmt"
	"sync"
)

// MemoryOverrideStore is the in-memory OverrideStore used by tests and
// by the dev-mode build of the API. Concurrency-safe; goroutine-safe
// to share across HTTP handlers.
//
// The production OverrideStore wraps the options table — see the
// PostgresOverrideStore in the apps/api wiring. Both implementations
// satisfy OverrideStore and are interchangeable at the handler level,
// which is the whole point of putting the interface here.
type MemoryOverrideStore struct {
	mu   sync.RWMutex
	data map[string]BlockTree // key = theme + "/" + name
}

// NewMemoryOverrideStore returns an empty MemoryOverrideStore.
func NewMemoryOverrideStore() *MemoryOverrideStore {
	return &MemoryOverrideStore{
		data: make(map[string]BlockTree),
	}
}

// Get returns the override BlockTree for (theme, name).
func (s *MemoryOverrideStore) Get(_ context.Context, theme, name string) (BlockTree, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tree, ok := s.data[overrideKey(theme, name)]
	if !ok {
		return nil, false, nil
	}
	// Defensive copy so a caller that mutates the returned tree
	// doesn't corrupt the store.
	out := make(BlockTree, len(tree))
	copy(out, tree)
	return out, true, nil
}

// Put upserts the override.
func (s *MemoryOverrideStore) Put(_ context.Context, theme, name string, tree BlockTree) error {
	if theme == "" {
		return fmt.Errorf("site_editor: Put: theme is required")
	}
	if name == "" {
		return fmt.Errorf("site_editor: Put: name is required")
	}
	cpy := make(BlockTree, len(tree))
	copy(cpy, tree)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[overrideKey(theme, name)] = cpy
	return nil
}

// Delete removes the override. Returns nil if no override exists —
// the operation is idempotent.
func (s *MemoryOverrideStore) Delete(_ context.Context, theme, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, overrideKey(theme, name))
	return nil
}

// OptionsKey returns the options-table key for an override. Exported
// so the production PostgresOverrideStore (in apps/api wiring) builds
// the same key shape — keeping the canonical form in one place means
// a future schema audit doesn't have to chase two definitions.
//
//	theme_mods.{theme}.parts.{name}
//
// Lower-case slugs are the convention; the function does NOT lowercase
// its arguments (the options table key is citext, which already
// folds case).
func OptionsKey(theme, name string) string {
	return "theme_mods." + theme + ".parts." + name
}

// overrideKey is the internal cache key for MemoryOverrideStore. Built
// from the same (theme, name) pair as OptionsKey so a slug switch
// invalidates the entries cleanly.
func overrideKey(theme, name string) string {
	return theme + "/" + name
}
