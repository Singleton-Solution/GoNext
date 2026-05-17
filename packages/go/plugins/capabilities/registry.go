package capabilities

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrAlreadyRegistered is returned by Registry.Register when the cap ID
// is already present. The contract is "first writer wins": the existing
// def is preserved, and the duplicate Register call surfaces the conflict
// via this error rather than panicking. The lifecycle install path
// expects to see this if a plugin's host wrapper accidentally re-registers
// a built-in.
//
// We do NOT panic on duplicates (unlike packages/go/policy's user-cap
// registry) because the plugin-cap registry is intended to be extended
// at runtime by host modules wired in at boot; an integration test that
// constructs two hosts in the same process would otherwise be impossible.
// First-write-wins is the principled choice when the entries are
// supposed to be idempotent.
var ErrAlreadyRegistered = errors.New("capabilities: capability already registered")

// ErrEmptyID is returned by Registry.Register when CapabilityDef.ID is
// empty. An empty ID would short-circuit every Checker.Allowed call to
// false in a way that's nearly impossible to debug from a log, so we
// reject it loudly at register time.
var ErrEmptyID = errors.New("capabilities: capability ID is required")

// Registry is the process-wide store of CapabilityDefs.
//
// Safe for concurrent Register / Get / List. Uses sync.RWMutex because
// the access pattern is read-heavy — every plugin host-call hits Get
// indirectly through a Checker, while Register only happens at process
// init and (rarely) when a new host module is wired in.
//
// Construct via NewRegistry. The package also exposes Default(), a
// process-wide singleton pre-seeded with the built-in cap set. Most
// production code should reach for Default; tests that want a clean
// slate construct their own.
type Registry struct {
	mu   sync.RWMutex
	defs map[string]CapabilityDef
}

// NewRegistry returns an empty Registry. The built-in caps are NOT
// pre-seeded — call SeedBuiltins explicitly, or use Default() which
// returns a pre-seeded singleton.
//
// Separating "empty" from "seeded" lets tests pick the seeding
// granularity they want: a unit test for Register doesn't want every
// built-in occupying the map.
func NewRegistry() *Registry {
	return &Registry{defs: map[string]CapabilityDef{}}
}

// Register adds def to the registry. Returns ErrEmptyID if def.ID is
// empty, ErrAlreadyRegistered (wrapped) if a def with the same ID
// already exists, or nil on success.
//
// First-writer-wins: a second Register call for the same ID does NOT
// overwrite the existing def. The caller observes the conflict via the
// returned error and can decide whether to fail loudly, log, or ignore.
//
// Safe for concurrent use.
func (r *Registry) Register(def CapabilityDef) error {
	if def.ID == "" {
		return ErrEmptyID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[def.ID]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, def.ID)
	}
	r.defs[def.ID] = def
	return nil
}

// Get returns the def for id and a bool indicating whether it was
// found. The returned CapabilityDef is a value copy — mutating it has
// no effect on the registry.
//
// Safe for concurrent use.
func (r *Registry) Get(id string) (CapabilityDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[id]
	return def, ok
}

// List returns every registered CapabilityDef, sorted by ID for
// determinism. The returned slice is a fresh copy; the caller may
// mutate it freely.
//
// Useful for admin UIs ("what caps exist?") and for the lifecycle
// install path's manifest-validation phase. Safe for concurrent use.
func (r *Registry) List() []CapabilityDef {
	r.mu.RLock()
	out := make([]CapabilityDef, 0, len(r.defs))
	for _, def := range r.defs {
		out = append(out, def)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Has is a convenience for "is this cap registered at all?". Equivalent
// to discarding the def returned by Get; provided so call sites that
// only care about membership read more naturally.
func (r *Registry) Has(id string) bool {
	_, ok := r.Get(id)
	return ok
}

// defaultRegistry is the process-wide singleton pre-seeded with the
// built-in cap set. Constructed once at init; never replaced. The
// init-time Register calls go through this registry.
var defaultRegistry = newDefaultRegistry()

// newDefaultRegistry constructs the seeded singleton. Split out so
// resetDefaultForTest can rebuild it without exporting the seeding
// helper.
func newDefaultRegistry() *Registry {
	r := NewRegistry()
	seedBuiltins(r)
	return r
}

// Default returns the process-wide Registry pre-seeded with built-in
// caps. Production code should reach for this; tests that want a clean
// slate construct their own via NewRegistry.
//
// Safe for concurrent use; the underlying *Registry is goroutine-safe.
func Default() *Registry { return defaultRegistry }

// seedBuiltins inserts the canonical built-in cap set into r. Called
// once from newDefaultRegistry and from tests that want a fresh seeded
// registry. Exported (lower-cased) inside the package so the package
// owns its built-in list — external callers should not be seeding their
// own copy of the built-ins.
//
// Any Register failure here is a programming error (duplicate ID in
// our own seed list) and panics. The seed list is a constant; if it
// ever has duplicates, the test suite catches it on the first run.
func seedBuiltins(r *Registry) {
	for _, def := range builtinCapabilityDefs() {
		if err := r.Register(def); err != nil {
			panic(fmt.Sprintf("capabilities: seedBuiltins: %v", err))
		}
	}
}

// builtinCapabilityDefs returns the canonical list of host capabilities
// available to plugins in P0. Grouped by resource for readability; the
// admin UI groups by Resource as well so the on-disk order doesn't
// matter, but keeping it ordered makes the seed easy to diff against
// docs/02-plugin-system.md §4.
//
// New caps land here. The plugin manifest format references them by
// CapabilityDef.ID; renaming an ID is a wire-format break.
func builtinCapabilityDefs() []CapabilityDef {
	return []CapabilityDef{
		// Posts — the canonical resource. read is non-sensitive; write
		// is gated but not flagged sensitive because the plugin is
		// scoped to its own posts namespace at the storage layer (when
		// that lands).
		{
			ID:          "posts.read",
			Description: "Read post rows.",
			Resource:    "posts",
			Action:      "read",
		},
		{
			ID:          "posts.write",
			Description: "Create, update, and delete post rows.",
			Resource:    "posts",
			Action:      "write",
		},

		// Users — read only in P0; the host does not expose a user.write
		// capability because account mutation belongs to the platform.
		// users.read intentionally excludes PII fields at the ABI layer;
		// the capability gates the call, not the projection.
		{
			ID:          "users.read",
			Description: "Read user rows (non-PII projection).",
			Resource:    "users",
			Action:      "read",
		},

		// Outbound effects — both sensitive. email.send and http.fetch
		// can be abused to exfiltrate data or send spam; the operator
		// should see a prominent warning when granting them.
		{
			ID:          "email.send",
			Description: "Send outbound transactional email.",
			Resource:    "email",
			Action:      "send",
			Sensitive:   true,
		},
		{
			ID:          "http.fetch",
			Description: "Make outbound HTTP requests.",
			Resource:    "http",
			Action:      "fetch",
			Sensitive:   true,
		},

		// KV — the per-plugin key-value namespace. Split read/write so
		// a plugin can declare read-only access to its own state for a
		// background-job worker without grabbing write privilege.
		{
			ID:          "kv.read",
			Description: "Read from the plugin key-value namespace.",
			Resource:    "kv",
			Action:      "read",
		},
		{
			ID:          "kv.write",
			Description: "Write to the plugin key-value namespace.",
			Resource:    "kv",
			Action:      "write",
		},

		// Hooks — gate the ability to subscribe to platform events.
		// A plugin that doesn't declare this cap cannot register any
		// hook listeners at instantiation.
		{
			ID:          "hooks.subscribe",
			Description: "Register a hook listener.",
			Resource:    "hooks",
			Action:      "subscribe",
		},

		// Jobs — gate the background-job enqueue ABI. Dequeue/run is
		// host-driven; only enqueue needs a capability.
		{
			ID:          "jobs.enqueue",
			Description: "Enqueue a background job.",
			Resource:    "jobs",
			Action:      "enqueue",
		},
	}
}

// resetDefaultForTest replaces the global Default registry with a fresh
// seeded one. Test-only; the unexported name keeps it out of the public
// surface but in-package tests can reach it.
func resetDefaultForTest() {
	defaultRegistry = newDefaultRegistry()
}
