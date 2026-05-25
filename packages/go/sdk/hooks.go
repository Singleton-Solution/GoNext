package sdk

import (
	"encoding/json"
	"sync"
)

// ActionHandler is the plugin-author-facing signature for an action
// hook handler. The args slice carries whatever the host bus passed
// via Bus.Do — JSON-decoded into []any. Returning a non-nil error
// surfaces as ResultStatusError to the host.
//
// The slice is OWNED by the SDK: handlers MUST NOT retain pointers
// into it past the call. The underlying storage is reused across hook
// dispatches to keep the per-call allocation rate down.
type ActionHandler func(args []any) error

// FilterHandler is the plugin-author-facing signature for a filter
// hook handler. value is the transformable input as raw JSON bytes;
// the return value is the (also raw JSON) transformed output. args
// carries any per-call extras the host bus added.
//
// Returning a non-nil error rolls the filter chain back: the host
// surfaces it as ResultStatusError and keeps the pre-filter value.
type FilterHandler func(value json.RawMessage, args []any) (json.RawMessage, error)

// registry is the package-level dispatch table. Built by
// RegisterAction / RegisterFilter at plugin init time, consulted by
// the gn_handle_hook entry point.
//
// We use a sync.RWMutex rather than a sync.Map because:
//   - Writes happen exclusively at init; the read side is hot.
//   - The host serializes calls into a plugin module so concurrent
//     reads are rare in practice, but the mutex lets us claim safety
//     without depending on Module.Call's mutex.
var registry = struct {
	mu      sync.RWMutex
	actions map[string]ActionHandler
	filters map[string]FilterHandler
}{
	actions: map[string]ActionHandler{},
	filters: map[string]FilterHandler{},
}

// RegisterAction binds name -> handler in the dispatch table. Calling
// it twice for the same name overwrites the previous handler — that's
// the documented behaviour so a plugin author can replace handlers
// during development without restarting the process.
//
// The host's lifecycle manager has already validated the action name
// is in the manifest's hooks.actions list before invoking it. Names
// that aren't registered surface as ResultStatusUnknownHook to the
// host.
func RegisterAction(name string, handler ActionHandler) {
	if handler == nil {
		return
	}
	registry.mu.Lock()
	registry.actions[name] = handler
	registry.mu.Unlock()
}

// RegisterFilter binds name -> handler in the dispatch table. Same
// dupe semantics as RegisterAction.
func RegisterFilter(name string, handler FilterHandler) {
	if handler == nil {
		return
	}
	registry.mu.Lock()
	registry.filters[name] = handler
	registry.mu.Unlock()
}

// lookupAction returns the registered action handler for name, or nil.
func lookupAction(name string) ActionHandler {
	registry.mu.RLock()
	h := registry.actions[name]
	registry.mu.RUnlock()
	return h
}

// lookupFilter returns the registered filter handler for name, or nil.
func lookupFilter(name string) FilterHandler {
	registry.mu.RLock()
	h := registry.filters[name]
	registry.mu.RUnlock()
	return h
}

// resetRegistry is the test-only helper to clear the dispatch table
// between cases. Not exported; tests inside the package call it via
// the file-name suffix _test.go.
func resetRegistry() {
	registry.mu.Lock()
	registry.actions = map[string]ActionHandler{}
	registry.filters = map[string]FilterHandler{}
	registry.mu.Unlock()
}

// DispatchHook is the kind-agnostic dispatcher the gn_handle_hook
// export calls. It demultiplexes on hookName, decodes the payload as
// either an action or a filter envelope (depending on which handler
// is registered), and returns the (resultBytes, status) pair the
// caller packs into the i64 return.
//
// Exported (PascalCase) because the wasm-target gn_handle_hook
// implementation lives in a separately-compiled file (hooks_wasm.go)
// and needs to reach in from there.
//
// Returns:
//   - (resultBytes, StatusOK) on a successful action — resultBytes is
//     always nil for an action (no body) but a successful filter
//     returns the FilterResult-encoded bytes.
//   - (nil, sentinel) on any failure path. The sentinel is the
//     negative status the SDK packs into the low half of the i64
//     return.
func DispatchHook(hookName string, payload []byte) ([]byte, ResultStatus) {
	if actionHandler := lookupAction(hookName); actionHandler != nil {
		p, err := UnmarshalActionPayload(payload)
		if err != nil {
			return nil, StatusBadPayload
		}
		if err := actionHandler(p.Args); err != nil {
			return nil, StatusError
		}
		return nil, StatusOK
	}
	if filterHandler := lookupFilter(hookName); filterHandler != nil {
		p, err := UnmarshalFilterPayload(payload)
		if err != nil {
			return nil, StatusBadPayload
		}
		newValue, err := filterHandler(p.Value, p.Args)
		if err != nil {
			return nil, StatusError
		}
		out, err := MarshalFilterResult(newValue)
		if err != nil {
			return nil, StatusError
		}
		return out, StatusOK
	}
	return nil, StatusUnknownHook
}

// PluginInit is the entry point a plugin author calls from main().
// It records the manifest (today only as a logging signal — the
// effective manifest is the one in manifest.json that ships in the
// bundle) so SDK-aware tooling can extract the declared surface from
// the running plugin.
//
// PluginInit MUST be called from main(), AFTER every RegisterAction /
// RegisterFilter call. Calling it before registrations is legal but
// the dispatch table will be incomplete when the host first invokes
// gn_handle_hook.
//
// Today PluginInit is intentionally lightweight: it serves as the
// well-known entry point name plugin authors aim for, and gives the
// SDK a future-proof place to add init-time host calls (e.g. handshake
// with the host, version negotiation, capability self-check) without
// breaking the plugin-author surface.
func PluginInit(m Manifest) {
	pluginManifestMu.Lock()
	pluginManifest = m
	pluginManifestMu.Unlock()
}

// pluginManifest is the manifest recorded by PluginInit. Today read
// only via Manifest() for debugging/dev tooling; future versions of
// the SDK may use it to drive runtime self-checks.
var (
	pluginManifestMu sync.RWMutex
	pluginManifest   Manifest
)

// CurrentManifest returns the manifest the running plugin registered
// via PluginInit. Useful for dev tooling that wants to introspect a
// plugin's declared surface without unpacking the bundle.
//
// Returns the zero Manifest if PluginInit has not yet been called.
func CurrentManifest() Manifest {
	pluginManifestMu.RLock()
	defer pluginManifestMu.RUnlock()
	return pluginManifest
}
