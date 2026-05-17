// Package lifecycle implements the plugin lifecycle state machine.
//
// Every plugin that lives in the GoNext platform moves through a small
// set of explicit states: Installed → Active → Inactive → (Uninstalled).
// Any failure on the way captures the error and parks the row in the
// Errored state so the operator can inspect what went wrong before
// deciding to retry or roll back.
//
// This package owns the state-machine + persistence half of the plugin
// system. It deliberately does NOT own:
//
//   - The WASM runtime that actually loads and runs plugin code (issue
//     #6). The lifecycle Manager wires runtime "load" and "unload" calls
//     through a small Runtime interface; production code will inject the
//     wazero-based implementation, tests inject a fake.
//   - Bundle extraction, manifest validation, or signature verification
//     (issue #44). The Manager accepts a Bundle value that has already
//     been parsed; the io.Reader form of Install is a thin wrapper that
//     today only reads the manifest and trusts the caller to have run
//     the heavyweight checks. When #44 lands, the BundleParser interface
//     is the seam.
//   - Plugin-supplied SQL migrations. The Migrator interface is wired in
//     but its production implementation is empty (no-op) until the WASM
//     runtime issue is ready to drive it.
//
// # State machine
//
// The transitions are intentionally narrow:
//
//	            ┌────────────┐
//	            │  Installed │ ← Install() lands here unconditionally
//	            └─────┬──────┘
//	      Activate()  │
//	            ┌─────▼──────┐
//	            │   Active   │
//	            └─────┬──────┘
//	     Deactivate() │
//	            ┌─────▼──────┐  ← Activate() reopens Active
//	            │  Inactive  │
//	            └─────┬──────┘
//	      Uninstall() │
//	            ┌─────▼──────────┐
//	            │ PendingUninst  │  ← cleanup runs here, then row deleted
//	            └────────────────┘
//
// Any state can fall into Errored when a runtime call (load, migrate,
// unload) fails. The transition into Errored is one-way for that
// operation — the operator must call Reset to bring the row back to
// Inactive after fixing whatever broke. Reset is the only transition
// that bypasses the "must come from previous state" rule.
//
// docs/02-plugin-system.md §3 specifies a slightly broader vocabulary
// (`absent`, `disabled_by_policy`, `failed`). We map them like this:
//
//   - `absent`  → the row has been deleted from storage; not a State value
//   - `failed`  → Errored (the lifecycle name; the doc and code agree on
//     semantics, the spelling differs)
//   - `disabled_by_policy` is out of scope here. Policy bans live in the
//     `policy` package and are checked at Install time, not modeled as
//     a state transition.
//
// # Concurrency
//
// Two operators clicking "Activate" at the same instant must not both
// win. The Storage.UpdateState contract enforces this with a conditional
// UPDATE: the new state lands only if the row's current state matches
// expectedFrom. A concurrent caller observing the same expectedFrom
// loses the race and gets ErrInvalidTransition. The in-memory storage
// implementation models the same semantics under a per-slug mutex; the
// Postgres implementation will use a single statement of the form:
//
//	UPDATE plugins
//	   SET state = $1, version = version + 1, updated_at = now()
//	 WHERE slug = $2
//	   AND state = $3;
//
// The Manager's transition methods always go through Storage.UpdateState
// rather than read-then-write so the race is closed at the database
// (or, in tests, at the mutex).
//
// # Auditing
//
// Every transition emits one audit row through an injected audit.Emitter:
//
//	plugin.installed     — successful Install
//	plugin.activated     — successful Activate
//	plugin.deactivated   — successful Deactivate
//	plugin.uninstalled   — successful Uninstall (after cleanup)
//	plugin.errored       — any transition that parked the row in Errored
//	plugin.reset         — operator-driven recovery from Errored
//
// Audit failures are logged but do not roll back the transition. The
// state change is the authoritative record; the audit row is a
// best-effort secondary index for human reviewers.
//
// # Plugins table (deferred migration)
//
// The Postgres Storage implementation expects a `plugins` table with the
// following columns. The CREATE TABLE migration ships separately (when
// the broader plugin system migrations land), but the column contract is
// frozen here so callers compile against a stable schema.
//
//	CREATE TABLE plugins (
//	    slug          TEXT PRIMARY KEY,
//	    version       TEXT NOT NULL,           -- semantic version from manifest
//	    abi_version   INT  NOT NULL,
//	    manifest      JSONB NOT NULL,
//	    state         TEXT NOT NULL,           -- one of the State enum values
//	    capabilities  JSONB NOT NULL DEFAULT '[]'::JSONB,
//	    last_error    TEXT NOT NULL DEFAULT '',
//	    error_at      TIMESTAMPTZ,
//	    installed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
//	    activated_at  TIMESTAMPTZ,
//	    row_version   BIGINT NOT NULL DEFAULT 1,   -- bumped by every UpdateState
//	    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
//	);
//	CREATE INDEX plugins_state_idx ON plugins (state);
//
// The `row_version` column exists so a future read-modify-write caller
// (e.g. the admin UI changing capabilities) can use optimistic
// concurrency. The lifecycle Manager itself doesn't depend on it; the
// state CAS is sufficient for our purposes.
//
// # Typical wiring
//
//	store := lifecycle.NewMemoryStorage()        // or NewPostgresStorage(pool)
//	mgr := lifecycle.NewManager(store, auditEmitter,
//	    lifecycle.WithRuntime(wasmRuntime),     // optional; no-op by default
//	    lifecycle.WithMigrator(pluginMigrator), // optional; no-op by default
//	    lifecycle.WithLogger(logger),
//	)
//
//	// Admin uploads a bundle, then activates explicitly.
//	slug, err := mgr.Install(ctx, bundleReader)
//	if err := mgr.Activate(ctx, slug); err != nil { ... }
//
// See docs/02-plugin-system.md §3 for the broader lifecycle picture.
package lifecycle
