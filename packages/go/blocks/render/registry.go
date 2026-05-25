package render

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrDuplicateBlockType is returned by Registry.Register when a block
// type is registered twice. Mirrors the TS DuplicateBlockTypeError so
// the editor-side and server-side experiences are uniform: a plugin
// that tries to clobber an existing block type gets a loud error,
// not silent breakage.
var ErrDuplicateBlockType = errors.New("render: block type already registered")

// Registry is the per-process dispatch table the Walker consults.
//
// Registries are safe for concurrent reads; Register and Unregister
// take an internal mutex so plugin activation/deactivation can race
// with an in-flight render without corrupting the underlying map.
// Reads use a sync.RWMutex so the hot path stays lock-free in
// principle (multiple parallel renders).
type Registry struct {
	mu    sync.RWMutex
	specs map[string]BlockSpec
}

// NewRegistry constructs an empty Registry. Callers typically
// follow up with RegisterCoreBlocks to seed the sixteen core
// renderers, then register plugin renderers atop that.
func NewRegistry() *Registry {
	return &Registry{specs: make(map[string]BlockSpec)}
}

// Register installs a BlockSpec under the given block type. The
// type string is taken verbatim — the registry imposes no parsing
// rule; namespacing ("core/heading", "wp-pricing/pricing-table") is
// the platform's convention, not the registry's.
//
// Register returns ErrDuplicateBlockType when `blockType` is already
// in the table. The HMR-friendly escape hatch is to call Unregister
// first; we don't expose a `replace: true` flag here because the
// production process never runs HMR — silent overwrites have caused
// real bugs in WordPress / Gutenberg and we'd rather not import that
// failure mode.
//
// Register also returns an error when spec.Render is nil — a
// registered block without a renderer is the same kind of bug as no
// registration at all, but harder to debug at render time.
func (r *Registry) Register(blockType string, spec BlockSpec) error {
	if blockType == "" {
		return errors.New("render: blockType is required")
	}
	if spec.Render == nil {
		return fmt.Errorf("render: block type %q: Render is nil", blockType)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[blockType]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateBlockType, blockType)
	}
	r.specs[blockType] = spec
	return nil
}

// MustRegister is Register that panics on error. Convenient for
// package init() chains that wire the core renderers — a duplicate
// registration there is a programmer error, not a runtime one.
func (r *Registry) MustRegister(blockType string, spec BlockSpec) {
	if err := r.Register(blockType, spec); err != nil {
		panic(err)
	}
}

// Get returns the BlockSpec registered for `blockType`. The second
// return is false when nothing is registered — the Walker uses this
// to fall back to its UnknownBlock placeholder.
func (r *Registry) Get(blockType string) (BlockSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[blockType]
	return spec, ok
}

// Has reports whether a renderer is registered under `blockType`.
// Cheap probe for callers that want to branch without touching the
// returned spec.
func (r *Registry) Has(blockType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.specs[blockType]
	return ok
}

// Unregister removes a registration, returning true if something
// was removed. Useful for tests and plugin teardown.
func (r *Registry) Unregister(blockType string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.specs[blockType]; !ok {
		return false
	}
	delete(r.specs, blockType)
	return true
}

// Names returns the registered block types in lexicographic order.
// Mostly useful for diagnostics and test assertions; the order is
// stable so a snapshot fixture doesn't churn.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.specs))
	for n := range r.specs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Len returns the number of registered block types.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.specs)
}
