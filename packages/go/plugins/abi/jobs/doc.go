// Package jobs implements the host-side half of the plugin job
// handler ABI: the bridge between the manifest-declared jobs[] field
// and the host TaskSpec + Asynq dispatch chassis.
//
// # Layered architecture
//
// The plugin job system has three pieces:
//
//	┌─────────────────────────────────────────────────────────────┐
//	│  packages/go/jobs/taskspec  — TaskSpec registry              │
//	│  packages/go/jobs/asynq     — Asynq server + queues          │
//	│  Owns dispatch, retries, queue weighting, metrics.           │
//	└─────────────────────────────────────────────────────────────┘
//	                              ▲
//	                              │ Registry.Register(TaskSpec{...})
//	┌─────────────────────────────────────────────────────────────┐
//	│  packages/go/plugins/abi/jobs  — THIS PACKAGE               │
//	│  Bridge: for each job in the manifest, install a TaskSpec    │
//	│  whose Handler proxies into the guest. Dispatcher: drive     │
//	│  the gn_handle_job ABI on a Module.                          │
//	└─────────────────────────────────────────────────────────────┘
//	                              ▲
//	                              │ Module.Call("gn_handle_job", ...)
//	┌─────────────────────────────────────────────────────────────┐
//	│  packages/go/plugins/runtime  — wazero host                  │
//	│  Compiles & instantiates WASM. Exposes gn_log/gn_panic/      │
//	│  gn_time_ms. Module.Call serializes against wazero's         │
//	│  per-instance rules and applies the limits.Enforcer.         │
//	└─────────────────────────────────────────────────────────────┘
//
// The job chassis has no idea plugins exist; the runtime has no idea
// about jobs. This package is the glue.
//
// # Symmetry with the hooks bridge
//
// The packages/go/plugins/abi/hooks package implements the SAME
// pattern for the action/filter bus. The two ABIs are intentionally
// parallel:
//
//	hooks:  gn_handle_hook(name, payload) -> packed result
//	jobs:   gn_handle_job (name, payload) -> packed result
//
// The packed-result layout, the allocator contract, the trap handling,
// and the per-call observer interfaces are identical. Plugin SDKs
// share most of their host-call plumbing between the two.
//
// The differences are deliberately narrow:
//
//   - Jobs are gated on the jobs.enqueue capability at Register time.
//     A plugin without that cap is rejected before any TaskSpec is
//     installed. Hooks are gated on hooks.subscribe at a separate
//     gate (the lifecycle Manager).
//
//   - The job envelope carries an idempotency key (the asynq Task ID)
//     so the guest can deduplicate side effects across retries. Hooks
//     are synchronous and have no retry semantic, so no key is needed.
//
//   - Jobs land on the "plugin" asynq queue by default; the queue
//     name is configurable per-bridge.
//
// # The ABI in one paragraph
//
// Every plugin that declares jobs exports a function named
// `gn_handle_job(name_ptr, name_len, payload_ptr, payload_len) -> i64`.
// The host writes the job name and a JSON-encoded envelope (idempotency
// key + retry count + producer payload) into guest memory (via the
// guest's `gn_alloc(size) -> ptr` export), calls the entry point, and
// reads back a packed (ptr, len) return — high 32 bits hold the
// result pointer in guest memory, low 32 bits hold either zero
// (success) or a negative ResultStatus (typed failure). See abi.go for
// the formal description, marshal.go for the envelope shape,
// dispatcher.go for the host driver, and bridge.go for the manifest-
// walking bridge.
//
// # Typical wiring
//
//	// During plugin activation:
//	mod, err := runtime.LoadModule(ctx, slug, wasmBytes)
//	if err != nil { return err }
//	disp := abijobs.NewDispatcher(mod)
//	bridge, err := abijobs.NewBridge(slug, disp, taskRegistry, capChecker)
//	if err != nil { return err }
//	if _, err := bridge.Register(ctx, manifest); err != nil { return err }
//
//	// During plugin deactivation:
//	bridge.Unregister()
//	mod.Close(ctx)
package jobs
