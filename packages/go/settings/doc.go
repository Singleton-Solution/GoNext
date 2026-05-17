// Package settings is the GoNext canonical settings registry: every
// configurable knob the platform (or a plugin) exposes is declared here
// once, with a JSON Schema describing the value's shape and constraints
// and a Store that persists the actual values into the options table.
//
// What's here:
//
//   - Setting is the schema: key, type, JSON Schema (2020-12), default,
//     autoload flag, admin-UI group, the capability required to change
//     it, and an optional Validator callback. The struct is the contract
//     between the admin UI (which renders forms from the schema), the
//     REST API (which validates writes against it), and the CLI (which
//     uses the schema to format `gonext option get foo` output).
//
//   - Registry is a process-wide, sync-safe registry of Setting values.
//     Core pre-registers the WP-equivalent settings (site.name, timezone,
//     locale, permalinks.format, …); plugins register their own at
//     activation. Duplicate keys are a programming error and surface as
//     a Register error rather than a silent overwrite.
//
//   - Store is the persistence interface (Read / Write / BulkRead /
//     LoadAutoload). Read returns the Setting's Default if the key is
//     not yet in the store, so callers never have to special-case
//     "first run". Write validates against the Setting's Schema and
//     calls the optional Validator before persisting.
//
//   - MemoryStore is the test backend: a sync.Map under the hood, zero
//     dependencies.
//
//   - PostgresStore writes to the options table from migration 000008.
//     Hot reads go through a sync.Map-backed L1 cache; Write invalidates
//     the cache entry explicitly (no TTL — invalidate-on-write is
//     correct here because settings change rarely and stale reads are
//     never acceptable).
//
// Why the registry is separate from the store
//
// The registry is the schema (immutable contract: "this key exists and
// looks like this"); the store is the values (mutable state: "right
// now, site.name is 'My Site'"). Splitting them means the schema is
// always available even when the database is unreachable (the admin UI
// can still render the form's structure during a DB outage), and a
// plugin can register its settings at activation without needing a DB
// round-trip to "discover" what it just declared.
//
// Plugin-extensibility
//
// Plugins call Register() during activation. The Registry is in-memory
// and rebuilt on every process restart, which is the correct shape:
// activating a plugin restarts (or at least re-initializes) its
// registration, so there's no need for the registry to outlive the
// process. The Store, by contrast, is durable — a setting's value
// persists across restarts.
//
// Usage:
//
//	reg := settings.NewRegistry()
//	settings.RegisterCore(reg)               // seed core settings
//	store := settings.NewPostgresStore(pool, reg)
//
//	// Read a setting with default fallback.
//	v, _ := store.Read(ctx, "core.site.name")
//
//	// Write a setting — validated against the schema.
//	err := store.Write(ctx, "core.site.name", "My GoNext Site")
//
// See docs/05-admin-api.md §2.6 for the full design.
package settings
