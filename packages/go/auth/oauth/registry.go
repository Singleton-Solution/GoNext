package oauth

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry holds the set of Provider implementations available to an
// install. There is no implicit global registry — callers construct a
// Registry at boot time, register each configured provider, and pass the
// resulting *Registry to the HTTP layer.
//
// Avoiding a package-level singleton is deliberate. It means tests can
// have multiple isolated registries running in parallel (t.Parallel
// friendly), and plugin-loaded providers always go through an explicit
// call site rather than a package-init side effect that's hard to reason
// about in a load-order audit.
//
// Registry is safe for concurrent reads and writes; Get and List can run
// alongside Register on a hot path without locking the caller out.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty Registry. The zero-value *Registry is
// usable too (its internal map is allocated lazily under the write lock)
// but the constructor makes the intent explicit at the call site.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds p to the registry under p.ID(). It returns:
//
//   - ErrInvalidProviderID if p.ID() is empty or contains characters
//     outside [a-z0-9_-]. The character set is restrictive on purpose:
//     IDs go into URL paths (/auth/oidc/<id>/callback) and into a TEXT
//     column in user_external_identities; locking them to a safe subset
//     means no escaping bugs at either site.
//
//   - ErrDuplicateProvider if a provider is already registered under
//     the same ID. Registration is monotonic: there is no Unregister
//     for live providers, because unregistering a provider while a
//     callback is in flight would race the redirect against the user
//     and emit a confusing "unknown provider" error. Tests that need a
//     fresh registry should construct a new one.
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return fmt.Errorf("%w: nil provider", ErrInvalidProviderID)
	}
	id := p.ID()
	if err := validateID(id); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[string]Provider)
	}
	if _, ok := r.providers[id]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateProvider, id)
	}
	r.providers[id] = p
	return nil
}

// Get returns the provider registered under id. Returns
// ErrProviderNotFound (wrapped with the id) if no provider matches.
//
// IDs are case-sensitive. Lookups for an unknown id are a normal
// control-flow path — a login page may iterate over the operator's
// configured provider list and skip the ones that aren't registered —
// so this is not logged or audited at the lookup site.
func (r *Registry) Get(id string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotFound, id)
	}
	return p, nil
}

// List returns all registered providers, sorted by ID for deterministic
// output. The returned slice is a fresh copy; mutating it does not
// affect the registry.
//
// Determinism matters: List feeds the "Continue with …" buttons on the
// login screen, and rendering the buttons in a stable order keeps the
// UI from flickering between provider orders on each request.
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID() < out[j].ID()
	})
	return out
}

// validateID enforces the [a-z0-9_-] character set for provider IDs.
//
// We hand-roll the check rather than pulling regexp because the rule is
// trivial and the regexp package is comparatively heavy for a single
// boot-time validation. Empty IDs and IDs starting with '-' are also
// rejected (the leading '-' would mis-parse if anyone ever decides to
// stuff the ID into a CLI flag).
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is empty", ErrInvalidProviderID)
	}
	if strings.HasPrefix(id, "-") {
		return fmt.Errorf("%w: id %q starts with '-'", ErrInvalidProviderID, id)
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return fmt.Errorf("%w: id %q char %d (%q) not in [a-z0-9_-]", ErrInvalidProviderID, id, i, string(r))
		}
	}
	return nil
}
