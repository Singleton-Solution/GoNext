// Package marketplace owns the catalogue-side data model that backs the
// future community plugin marketplace.
//
// The plugin runtime + lifecycle packages (Waves D–G) cover the "I have
// a bundle on disk; install it" path. They are sufficient for an
// operator hand-loading a plugin into a single CMS instance but they do
// not model the discovery, versioning, ratings, and telemetry that a
// public marketplace needs. This package fills that gap with a small,
// boring SQL-backed store:
//
//	plugin_listings        — public-facing catalogue rows
//	plugin_versions        — per-listing artefact records (sha256 + manifest)
//	plugin_compat_matrix   — host ABI compatibility claims
//	plugin_ratings         — 1–5 star user ratings, one per (version, user)
//	plugin_install_events  — append-only telemetry
//
// The SQL schema lives in migrations 000018–000022; the column
// contracts are the source of truth and are heavily commented there.
//
// # What this package owns
//
// Five entry points, one per table:
//
//   - Listings — Create / Get / GetBySlug / List / ListByCategory /
//     Update / Delete.
//   - Versions — Publish (computes sha256 from supplied wasm bytes),
//     Get, ListByListing, Deprecate.
//   - CompatMatrix — Upsert, ListByVersion. Range overlap detection is
//     left to the caller because the right answer depends on host
//     semantics that don't belong here.
//   - Ratings — Submit (UPSERT, one per user per version), Aggregate
//     (avg + count for a version).
//   - Events — RecordInstallEvent (append-only); the read side is a
//     small set of aggregate queries used by the marketplace UI.
//
// # What this package does NOT own
//
//   - The wasm bytes themselves. Publish takes []byte and computes the
//     digest; the caller is expected to push the bytes to object storage
//     keyed by that digest. This package never re-reads the artefact.
//   - The manifest schema. The manifest blob is stored verbatim; the
//     validation surface lives in plugins/manifest.
//   - The marketplace HTTP surface. Routes/handlers belong in a future
//     web package; this package is store-only so it can be reused by a
//     CLI or a background worker.
//   - The runtime side of "install this plugin". That lives in
//     plugins/lifecycle. The marketplace Store records the catalogue
//     state; the lifecycle Manager installs the bundle.
//
// # Concurrency
//
// All Store methods are safe for concurrent use. Mutating writes use
// single-statement SQL where possible (the UPSERT shape for ratings,
// the conditional UPDATE shape for status transitions). The schema's
// UNIQUE constraints (listing slug, (listing_id, version)) close the
// race window for double-publishes.
//
// # Errors
//
// Three sentinels live in errors.go:
//
//   - ErrNotFound       — Get-shaped methods on a missing row.
//   - ErrAlreadyExists  — Insert-shaped methods on a slug/version clash.
//   - ErrInvalidInput   — argument validation failed before the SQL ran.
//
// Everything else is returned wrapped with %w so callers can errors.Is
// against the sentinel and unwrap to the underlying pgx/pgconn error
// for logging.
package marketplace
