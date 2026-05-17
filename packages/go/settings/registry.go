package settings

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ErrDuplicateKey is returned by Registry.Register when the Setting's
// Key collides with an already-registered key. It is distinct from
// ErrInvalidSchema and other validation errors so callers (especially
// plugin loaders) can present a sensible error message: "two plugins
// registered the same setting" is a different operator problem from
// "your schema is malformed".
var ErrDuplicateKey = errors.New("settings: duplicate key")

// ErrInvalidSchema is returned by Registry.Register when the Setting's
// Schema is missing, empty, or fails to compile as a JSON Schema. This
// is always a programming error — a Setting without a valid schema is
// useless (the API layer can't validate writes, the admin UI can't
// render a form), so we fail fast at registration rather than at first
// use.
var ErrInvalidSchema = errors.New("settings: invalid schema")

// ErrEmptyKey is returned when a Setting is registered with an empty
// Key. The empty string is the zero value of `string`; accepting it
// here would mean a typo silently registers a setting that no one
// can address.
var ErrEmptyKey = errors.New("settings: empty key")

// ErrInvalidType is returned when a Setting is registered with a Type
// that isn't one of the recognized SettingType values. See
// SettingType.Valid for the recognized set.
var ErrInvalidType = errors.New("settings: invalid type")

// ErrNotFound is returned by Registry.Get when no setting with the
// given key has been registered. Callers commonly translate this to
// an HTTP 404 on `GET /api/v1/settings/<key>`.
var ErrNotFound = errors.New("settings: not found")

// registryEntry pairs a Setting with its pre-compiled JSON Schema. The
// compile step is moderately expensive (allocates a tree of validators,
// resolves $refs, etc.), so we do it exactly once per Register call
// and reuse the *jsonschema.Schema on every Write.
type registryEntry struct {
	Setting Setting
	Schema  *jsonschema.Schema
}

// Registry is the process-wide store of Setting declarations. It is
// the schema half of the package (the value half is Store).
//
// Concurrency: every method is safe for use from many goroutines. The
// common pattern is bulk Register at boot (single-threaded), then
// concurrent Get/List from request handlers.
//
// A zero Registry is NOT usable — call NewRegistry. Package-level
// helpers (Register, Get, List, ListByGroup) operate on a process-wide
// global registry for callers that don't want to plumb one through.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*registryEntry
}

// NewRegistry returns an empty registry ready for use.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*registryEntry)}
}

// Register adds s to the registry. Returns ErrDuplicateKey if the key
// is already present, ErrEmptyKey/ErrInvalidType/ErrInvalidSchema if
// the Setting is malformed. The Setting is stored by value — mutating
// the original after Register has no effect on the registry copy.
//
// The Setting's Schema is compiled here. A malformed schema is
// detected at registration time, not at first Write — that means a
// broken schema in a plugin manifest is a boot-time error, not a
// confusing runtime 500.
func (r *Registry) Register(s Setting) error {
	if strings.TrimSpace(s.Key) == "" {
		return ErrEmptyKey
	}
	if !s.Type.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidType, s.Type)
	}
	if len(s.Schema) == 0 {
		return fmt.Errorf("%w: schema for key %q is empty", ErrInvalidSchema, s.Key)
	}

	compiled, err := compileSchema(s.Key, s.Schema)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[s.Key]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateKey, s.Key)
	}
	r.entries[s.Key] = &registryEntry{Setting: s, Schema: compiled}
	return nil
}

// MustRegister calls Register and panics if it returns an error. Use
// this in init() blocks where a registration failure is a build/release
// bug, not a runtime concern (e.g. core settings, well-tested plugins).
// Production code that loads schemas from a plugin manifest at runtime
// should prefer Register and surface the error to the operator.
func (r *Registry) MustRegister(s Setting) {
	if err := r.Register(s); err != nil {
		panic(fmt.Sprintf("settings: MustRegister(%q): %v", s.Key, err))
	}
}

// Get returns the Setting registered for key. If the key is not
// registered, Get returns the zero Setting and ErrNotFound.
func (r *Registry) Get(key string) (Setting, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[key]
	if !ok {
		return Setting{}, fmt.Errorf("%w: %q", ErrNotFound, key)
	}
	return entry.Setting, nil
}

// List returns every registered Setting, sorted by Key. Useful for
// admin UIs, CLI `gonext option list`, and bulk diagnostics. The
// returned slice is a fresh copy — mutating it does not affect the
// registry.
func (r *Registry) List() []Setting {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Setting, 0, len(r.entries))
	for _, entry := range r.entries {
		out = append(out, entry.Setting)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ListByGroup returns every registered Setting whose Group equals
// group, sorted by Key. The empty string matches Settings with no
// Group declared (which Registry treats as the uncategorized bucket).
//
// Used by the admin UI's per-page renderer: each settings page calls
// ListByGroup("general") / ("reading") / etc. and gets back exactly
// the fields that page should render.
func (r *Registry) ListByGroup(group string) []Setting {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Setting, 0)
	for _, entry := range r.entries {
		if entry.Setting.Group == group {
			out = append(out, entry.Setting)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// settingFor is the package-internal "Get without copying" path used
// by Store.Write to access both Setting and compiled schema in a
// single lock acquisition.
func (r *Registry) settingFor(key string) (*registryEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[key]
	return entry, ok
}

// compileSchema compiles a JSON Schema 2020-12 document. The key
// argument is used only as a stable identifier for $ref resolution and
// error messages — it is not a URL the compiler dereferences.
func compileSchema(key string, raw []byte) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	// AddResource gives the schema a stable URI so a panic-y compiler
	// doesn't fall back to filesystem lookups for an unspecified key.
	resourceURL := "https://gonext.local/settings/" + key + ".json"
	if err := c.AddResource(resourceURL, strings.NewReader(string(raw))); err != nil {
		return nil, fmt.Errorf("add resource: %w", err)
	}
	schema, err := c.Compile(resourceURL)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	return schema, nil
}

// globalRegistry is the package-level default registry. Code that
// doesn't want to thread a *Registry through every call uses the
// package-level Register / Get / List / ListByGroup helpers, which
// delegate to this. Tests that need isolation should construct their
// own *Registry via NewRegistry and avoid the global.
var globalRegistry = NewRegistry()

// Register adds s to the process-wide registry. See Registry.Register.
func Register(s Setting) error { return globalRegistry.Register(s) }

// MustRegister adds s to the process-wide registry and panics on error.
// See Registry.MustRegister.
func MustRegister(s Setting) { globalRegistry.MustRegister(s) }

// Get returns the Setting for key from the process-wide registry. See
// Registry.Get.
func Get(key string) (Setting, error) { return globalRegistry.Get(key) }

// List returns every Setting in the process-wide registry. See
// Registry.List.
func List() []Setting { return globalRegistry.List() }

// ListByGroup returns Settings in the process-wide registry with the
// given Group. See Registry.ListByGroup.
func ListByGroup(group string) []Setting { return globalRegistry.ListByGroup(group) }

// resetGlobalRegistryForTest replaces the package-global registry with
// a fresh one. Test-only — the unexported name keeps it off the public
// surface; in-package tests reach for it directly.
func resetGlobalRegistryForTest() {
	globalRegistry = NewRegistry()
}
