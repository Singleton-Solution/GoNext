// Package hooks implements the host-side half of the plugin hook
// handler ABI: the bridge between the manifest-declared hook
// subscriptions and the host hook bus.
//
// # Layered architecture
//
// The hook system has three pieces:
//
//	┌─────────────────────────────────────────────────────────────┐
//	│  packages/go/hooks  — Bus (action+filter dispatcher)         │
//	│  Owns chains, fan-out, priorities, panic recovery.           │
//	└─────────────────────────────────────────────────────────────┘
//	                              ▲
//	                              │ RegisterAction / RegisterFilter
//	┌─────────────────────────────────────────────────────────────┐
//	│  packages/go/plugins/abi/hooks  — THIS PACKAGE              │
//	│  Bridge: for each hook in the manifest, install a host-side │
//	│  callback that proxies into the guest. Dispatcher: drive    │
//	│  the gn_handle_hook ABI on a Module.                        │
//	└─────────────────────────────────────────────────────────────┘
//	                              ▲
//	                              │ Module.Call("gn_handle_hook", ...)
//	┌─────────────────────────────────────────────────────────────┐
//	│  packages/go/plugins/runtime  — wazero host                  │
//	│  Compiles & instantiates WASM. Exposes gn_log/gn_panic/      │
//	│  gn_time_ms. Module.Call serializes against wazero's         │
//	│  per-instance rules.                                         │
//	└─────────────────────────────────────────────────────────────┘
//
// The bus has no idea plugins exist; the runtime has no idea about
// hooks. This package is the glue.
//
// # The ABI in one paragraph
//
// Every plugin exports a function named `gn_handle_hook(name_ptr,
// name_len, payload_ptr, payload_len) -> i64`. The host writes the
// hook name and a JSON-encoded payload into guest memory (via the
// guest's `gn_alloc(size) -> ptr` export), calls the entry point, and
// reads back a packed (ptr, len) return — high 32 bits hold the result
// pointer in guest memory, low 32 bits hold either a non-negative
// length (success with body) or a negative ResultStatus (typed
// failure). Special case (0, 0) is "success, no body" — the action
// return shape.
//
// See abi.go for the formal description, marshal.go for the JSON
// envelopes, dispatcher.go for the host driver, and registry.go for
// the manifest-walking bridge.
//
// # Typical wiring
//
//	// During plugin activation:
//	mod, err := runtime.LoadModule(ctx, slug, wasmBytes)
//	if err != nil { return err }
//	disp := abihooks.NewDispatcher(mod)
//	bridge, err := abihooks.NewBridge(slug, disp, hostBus)
//	if err != nil { return err }
//	if _, err := bridge.Register(ctx, manifest); err != nil { return err }
//
//	// During plugin deactivation:
//	bridge.Unregister()
//	mod.Close(ctx)
package hooks
