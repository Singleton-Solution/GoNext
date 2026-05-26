// Package customfields owns the JSON-Schema-defined field group +
// per-post meta-value storage that powers GoNext's equivalent of
// Advanced Custom Fields. Field groups are operator- and plugin-
// authored declarations of "what extra fields does THIS post type
// gain" — title, body, and the standard post columns are core; field
// groups extend them with structured metadata (product price, event
// date, ACF-style repeaters).
//
// The package surface:
//
//   - FieldGroup: a JSON Schema (draft 2020-12) describing one
//     group's fields. The group's `target` selects which post types
//     it applies to (defaults to "any"); the `definition` is a full
//     JSON Schema object passed to jsonschemautil.Compile for
//     validation.
//
//   - MetaStore: the per-post meta-value persistence interface. The
//     production backend is Postgres (one row per (post_id, group_id,
//     key) tuple via a JSONB blob); tests use the in-memory store.
//
//   - Validate: applies the field group's compiled schema to a meta
//     blob, returning a multi-error so the caller can surface every
//     violation at once rather than play whack-a-mole.
//
// Why a separate package: the existing migrate/acf package translates
// WordPress ACF group definitions INTO this package's FieldGroup
// type. Keeping the runtime + storage separate from the importer
// preserves the layering that "I want custom fields at runtime
// without ever touching ACF" works.
//
// REST surface (mounted by apps/api/internal/rest/customfields):
//
//	GET    /api/v1/custom-fields/groups            — list field groups
//	POST   /api/v1/custom-fields/groups            — create
//	GET    /api/v1/custom-fields/groups/{id}       — fetch one
//	PATCH  /api/v1/custom-fields/groups/{id}       — update
//	DELETE /api/v1/custom-fields/groups/{id}       — delete
//
//	GET    /api/v1/posts/{post_id}/meta            — list meta values
//	GET    /api/v1/posts/{post_id}/meta/{group_id} — fetch one group's values
//	PUT    /api/v1/posts/{post_id}/meta/{group_id} — replace values
package customfields
