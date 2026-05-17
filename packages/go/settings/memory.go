package settings

import (
	"context"
	"sync"
)

// MemoryStore is an in-process Store backed by a map. It exists for
// unit tests and short-lived scenarios (CLI dry-runs, schema
// validation in a build step). It is NOT a fit for production — there
// is no persistence, no replication, and no eviction.
//
// Concurrency: safe for concurrent Read / Write / BulkRead /
// LoadAutoload. Guarded by a single sync.RWMutex; contention is
// acceptable at test scale.
type MemoryStore struct {
	mu       sync.RWMutex
	values   map[string]any
	registry *Registry
}

// NewMemoryStore returns an empty MemoryStore bound to reg. The store
// holds a pointer to reg; subsequent registrations on reg are visible
// to the store immediately, which is the right shape for tests that
// register settings ad-hoc.
func NewMemoryStore(reg *Registry) *MemoryStore {
	return &MemoryStore{
		values:   make(map[string]any),
		registry: reg,
	}
}

// Read returns the stored value for key, or the registered Default if
// the key is not yet stored.
//
// Returns ErrUnknownKey if the key is not registered — we refuse to
// invent values for keys that don't exist in the registry, because
// the caller has no schema to interpret what came back.
func (s *MemoryStore) Read(_ context.Context, key string) (any, error) {
	entry, ok := s.registry.settingFor(key)
	if !ok {
		return nil, ErrUnknownKey
	}
	s.mu.RLock()
	v, found := s.values[key]
	s.mu.RUnlock()
	if !found {
		return entry.Setting.Default, nil
	}
	return v, nil
}

// Write validates value against the registered Setting's Schema (and
// Validator) and stores it. Returns ErrUnknownKey if the key is not
// registered, ErrValidation (wrapped) on validation failure.
func (s *MemoryStore) Write(_ context.Context, key string, value any) error {
	entry, ok := s.registry.settingFor(key)
	if !ok {
		return ErrUnknownKey
	}
	if err := validate(entry, value); err != nil {
		return err
	}
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return nil
}

// BulkRead returns current values for the requested keys, defaulting
// to the registered Default for any key not yet written. Unknown keys
// (not in the registry) are silently skipped.
func (s *MemoryStore) BulkRead(_ context.Context, keys []string) (map[string]any, error) {
	out := make(map[string]any, len(keys))
	s.mu.RLock()
	for _, key := range keys {
		entry, ok := s.registry.settingFor(key)
		if !ok {
			continue
		}
		if v, found := s.values[key]; found {
			out[key] = v
		} else {
			out[key] = entry.Setting.Default
		}
	}
	s.mu.RUnlock()
	return out, nil
}

// LoadAutoload returns values for every registered Setting where
// Autoload=true, applying defaults for keys not yet written.
func (s *MemoryStore) LoadAutoload(_ context.Context) (map[string]any, error) {
	// Snapshot the registry's autoload set under its lock, then read
	// values under ours — avoid holding both locks at once.
	autoloadKeys := make(map[string]any, 16)
	for _, setting := range s.registry.List() {
		if setting.Autoload {
			autoloadKeys[setting.Key] = setting.Default
		}
	}

	s.mu.RLock()
	for key, def := range autoloadKeys {
		if v, found := s.values[key]; found {
			autoloadKeys[key] = v
		} else {
			autoloadKeys[key] = def
		}
	}
	s.mu.RUnlock()
	return autoloadKeys, nil
}

// Ensure MemoryStore satisfies Store at compile time.
var _ Store = (*MemoryStore)(nil)
