// Package acf parses Advanced Custom Fields (ACF) JSON exports and maps
// them into GoNext's field-group schema and post_meta value rows.
//
// ACF is the most-installed custom-fields plugin in the WordPress
// ecosystem; it ships its field group definitions as JSON files under
// wp-content/plugins/advanced-custom-fields/acf-json/ (or the Pro
// equivalent). When migrating a WordPress site to GoNext we want to
// preserve those field-group definitions AND the per-post values that
// authors have already populated against them.
//
// This package is intentionally read-only: we never write back to
// ACF's storage format. It exposes:
//
//	Parse(io.Reader) (*FieldGroupExport, error)
//	MapFieldGroup(*FieldGroup) (*Schema, error)
//	MapPostValues(*FieldGroup, postMeta map[string]string) ([]Value, error)
//
// Supported field types (best-effort):
//
//	text, textarea, number, email, url        — copied as-is
//	true_false                                 — coerced to boolean
//	select, radio                              — emitted as enum
//	image, file                                — links to the media table
//	                                             via attachment_id (the importer
//	                                             rewrites WP IDs → GoNext UUIDs)
//	post_object, relationship, page_link       — emitted as a reference list
//	user                                       — emitted as a user reference
//	repeater                                   — flattened to a JSON array of
//	                                             sub-field objects
//	flexible_content                           — flattened layout-by-layout
//
// Unknown field types are logged via the supplied Logger and skipped
// with no error. The expectation is operators inspect the report,
// confirm the misses are acceptable, and re-run.
//
// See issue #207 for the full migration plan, and
// docs/08-migration-compat.md §17 for the field-group schema this
// package targets.
package acf
