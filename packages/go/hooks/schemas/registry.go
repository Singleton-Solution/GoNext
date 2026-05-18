package schemas

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/Singleton-Solution/GoNext/packages/go/jsonschemautil"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ErrUnregisteredHook is returned by [Registry.ValidatePayload] when the
// hook name has no schema registered AND the registry is in strict mode.
// In loose mode (the default) unknown hooks return a nil error from
// ValidatePayload so consumers can layer the validator into untyped paths
// without breaking them.
//
// Callers can errors.Is against this sentinel to distinguish "no contract
// declared" from "payload didn't match the contract" — useful in tests
// asserting that a plugin host has registered every documented hook.
var ErrUnregisteredHook = errors.New("schemas: hook has no registered schema")

// ErrInvalidPayload is the umbrella error returned by
// [Registry.ValidatePayload] for any shape mismatch. Wrapped errors carry
// the underlying *jsonschema.ValidationError so callers wanting the
// per-instance-path details can errors.As to it.
//
// The bus surface uses errors.Is(err, ErrInvalidPayload) to count the
// "payload rejected" metric without parsing message strings.
var ErrInvalidPayload = errors.New("schemas: payload does not match hook schema")

// ErrSchemaAlreadyRegistered is returned by [SchemaRegistry.Register] when
// the same hook name is registered twice. Plugin hosts should reload the
// registry by constructing a fresh [Registry] rather than mutating in
// place — a duplicate Register is treated as a programmer error so
// double-registration doesn't silently shadow the earlier (often more
// authoritative) schema.
var ErrSchemaAlreadyRegistered = errors.New("schemas: hook schema already registered")

// SchemaRegistry holds the per-hook compiled schemas. Two reads are
// lock-free; writes (Register) serialize through a mutex but publish via
// an atomic pointer so concurrent Validate calls never observe a
// half-built map.
//
// The zero value is NOT ready for use — call [NewRegistry].
//
// The type is exported under the name SchemaRegistry to match the issue
// brief; the shorter alias [Registry] is provided for ergonomic usage at
// call sites (and is what most of the bus code references).
type SchemaRegistry struct {
	// schemas is the published, immutable snapshot. Readers Load this
	// pointer once and look up by name; writers prepare a new map under
	// the mutex and atomically Store it. The pattern matches what
	// hooks.Bus does for its chain slots — same trade-off, same shape.
	schemas atomic.Pointer[map[string]*jsonschema.Schema]

	// mu serializes Register. The registry is write-rare/read-heavy so
	// a Mutex is fine; we don't need RWMutex when readers don't take
	// the lock at all.
	mu sync.Mutex
}

// Registry is the short alias for [SchemaRegistry]. The bus signatures use
// *Registry to keep call sites readable; both names refer to the exact
// same type.
type Registry = SchemaRegistry

// NewRegistry returns an empty, ready-to-use registry. Use
// [BuiltinRegistry] instead if you want the WP-compat schemas pre-loaded.
func NewRegistry() *Registry {
	r := &Registry{}
	empty := map[string]*jsonschema.Schema{}
	r.schemas.Store(&empty)
	return r
}

// Register compiles schemaJSON under the pinned 2020-12 dialect and
// stores it for hookName. Returns an error if:
//
//   - hookName is empty (programmer error — refuse silently shadowing
//     the global "*" or similar accidents);
//   - schemaJSON declares a non-2020-12 $schema URL (wraps
//     [jsonschemautil.ErrUnsupportedDialect]);
//   - schemaJSON fails to compile;
//   - hookName is already registered (wraps
//     [ErrSchemaAlreadyRegistered]).
//
// Safe for concurrent use. After Register returns, subsequent
// [Registry.ValidatePayload] calls on hookName from any goroutine see
// the new schema.
//
// The compiled schema's resource ID is derived from hookName:
// "https://gonext.local/hooks/<name>.json". This is opaque to callers
// — it is only used for $ref resolution inside the schema itself.
func (r *SchemaRegistry) Register(hookName string, schemaJSON []byte) error {
	if hookName == "" {
		return errors.New("schemas: Register: empty hook name")
	}
	if len(schemaJSON) == 0 {
		return errors.New("schemas: Register: empty schema document")
	}

	resourceID := fmt.Sprintf("https://gonext.local/hooks/%s.json", hookName)
	compiled, err := jsonschemautil.Compile(resourceID, schemaJSON)
	if err != nil {
		return fmt.Errorf("schemas: Register(%q): %w", hookName, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	cur := r.schemas.Load()
	if cur != nil {
		if _, ok := (*cur)[hookName]; ok {
			return fmt.Errorf("%w: %q", ErrSchemaAlreadyRegistered, hookName)
		}
	}
	// Copy-on-write: build a fresh map containing all previous entries
	// plus the new one, then Store the pointer. Readers that observed
	// the old pointer continue to see the old map — no torn reads.
	next := make(map[string]*jsonschema.Schema, len(*cur)+1)
	for k, v := range *cur {
		next[k] = v
	}
	next[hookName] = compiled
	r.schemas.Store(&next)
	return nil
}

// MustRegister panics on error. Intended for init() use in the built-in
// schema bootstrap — failure there is a build-time bug, not a runtime
// condition. Application code should always call [Register].
func (r *SchemaRegistry) MustRegister(hookName string, schemaJSON []byte) {
	if err := r.Register(hookName, schemaJSON); err != nil {
		panic(err)
	}
}

// Has reports whether hookName has a registered schema. Cheap — single
// atomic load and a map probe. Used by [Enforce] to decide between strict
// rejection and loose pass-through.
func (r *SchemaRegistry) Has(hookName string) bool {
	cur := r.schemas.Load()
	if cur == nil {
		return false
	}
	_, ok := (*cur)[hookName]
	return ok
}

// Names returns the set of registered hook names, sorted lexically. The
// returned slice is a snapshot; mutating it has no effect on the registry.
// Used by tests and by the docs generator.
func (r *SchemaRegistry) Names() []string {
	cur := r.schemas.Load()
	if cur == nil {
		return nil
	}
	out := make([]string, 0, len(*cur))
	for k := range *cur {
		out = append(out, k)
	}
	// Sort imported via the standard library is overkill — we keep
	// stability cheap via a custom insertion since the registry size
	// is small (~20 hooks). For larger sets the caller is welcome to
	// sort externally.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j-1] > out[j] {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}

// lookup returns the compiled schema for hookName or nil. Internal; the
// exported entry is [ValidatePayload].
func (r *SchemaRegistry) lookup(hookName string) *jsonschema.Schema {
	cur := r.schemas.Load()
	if cur == nil {
		return nil
	}
	return (*cur)[hookName]
}
