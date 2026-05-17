package taskspec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrEmptyName is returned by Registry.Register when TaskSpec.Name is
// empty. An empty name would silently shadow asynq's default routing
// and produce tasks that the dispatch layer cannot handle, so we reject
// it loudly at register time rather than chase the bug at runtime.
var ErrEmptyName = errors.New("taskspec: task name is required")

// ErrAlreadyRegistered is returned by Registry.Register when a spec
// with the same Name is already present. The contract is first-writer-
// wins: the existing spec is preserved and the duplicate call surfaces
// the conflict via this error.
//
// We do NOT panic on duplicates (unlike packages/go/policy's user-cap
// registry) because TaskSpecs are intended to be extended at runtime
// by host modules wired in at boot; an integration test that constructs
// two app servers in the same process would otherwise be impossible.
// First-write-wins is the principled choice when the entries are
// supposed to be idempotent.
var ErrAlreadyRegistered = errors.New("taskspec: task name already registered")

// Registry is the process-wide store of TaskSpecs.
//
// Safe for concurrent Register / Get / Names. Uses sync.RWMutex because
// the access pattern is read-heavy: every Enqueue call hits Get, every
// worker-side Dispatch walks Names, while Register only happens at
// process init.
//
// Construct via NewRegistry for a clean instance, or use Default() for
// the process-wide singleton. Production wiring should reach for
// Default; tests that want isolation construct their own.
type Registry struct {
	mu    sync.RWMutex
	specs map[string]TaskSpec
}

// NewRegistry returns an empty Registry. There are no built-in
// TaskSpecs to pre-seed — every spec is declared by the package that
// owns its handler (webhooks/delivery owns webhook.deliver,
// email/send owns email.send, etc.), so the registry starts empty and
// fills up as packages call Register from their init.
func NewRegistry() *Registry {
	return &Registry{specs: map[string]TaskSpec{}}
}

// Register adds spec to the registry. Returns ErrEmptyName if spec.Name
// is empty, ErrAlreadyRegistered (wrapped with the offending name) if
// a spec with the same Name already exists, or nil on success.
//
// First-writer-wins: a second Register for the same Name does NOT
// overwrite the existing spec. The caller observes the conflict via
// the returned error and can decide whether to fail loudly, log, or
// ignore.
//
// Safe for concurrent use.
func (r *Registry) Register(spec TaskSpec) error {
	if spec.Name == "" {
		return ErrEmptyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[spec.Name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, spec.Name)
	}
	r.specs[spec.Name] = spec
	return nil
}

// Get returns the spec for name and a bool indicating whether it was
// found. The returned TaskSpec is a value copy — mutating it has no
// effect on the registry. (The PayloadSchema pointer is shared, but the
// underlying *jsonschema.Schema is documented by the upstream library
// as safe for concurrent Validate calls.)
//
// Safe for concurrent use.
func (r *Registry) Get(name string) (TaskSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[name]
	return spec, ok
}

// Names returns every registered task name, sorted lexicographically
// for determinism. The returned slice is a fresh copy; the caller may
// mutate it freely.
//
// Useful for admin UIs ("what tasks exist?"), for Dispatch (which
// iterates over Names to wire each spec onto a mux), and for
// diagnostics. Safe for concurrent use.
func (r *Registry) Names() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.specs))
	for name := range r.specs {
		out = append(out, name)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

// Has is a convenience for "is this task name registered?". Equivalent
// to discarding the spec returned by Get; provided so call sites that
// only care about membership read more naturally.
func (r *Registry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// defaultRegistry is the process-wide singleton. Constructed once at
// init; never replaced. Production packages register their specs into
// this registry from their package init.
var defaultRegistry = NewRegistry()

// Default returns the process-wide singleton Registry. Production code
// should reach for this; tests that want isolation construct their own
// via NewRegistry.
//
// Safe for concurrent use; the underlying *Registry is goroutine-safe.
func Default() *Registry { return defaultRegistry }

// resetDefaultForTest replaces the global Default registry with a
// fresh empty one. Test-only; the unexported name keeps it out of the
// public surface but in-package tests can reach it.
func resetDefaultForTest() {
	defaultRegistry = NewRegistry()
}
