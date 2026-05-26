// Package terms serves the public read-only `/api/v1/terms` REST
// surface. Terms are GoNext's taxonomy entries — categories, tags,
// and any plugin-registered hierarchical or flat taxonomy.
//
// The endpoint shape is two levels:
//
//	GET  /api/v1/taxonomies               — list registered taxonomies
//	GET  /api/v1/taxonomies/{slug}        — fetch one taxonomy's metadata
//	GET  /api/v1/terms                    — list terms across taxonomies
//	GET  /api/v1/terms/{id}               — fetch a single term
//
// In practice Mount handles "terms" only; the taxonomies surface is a
// lightweight sibling Mount because the two share a Store interface.
//
// Filter parameters on the list path:
//
//	?taxonomy=category        — restrict to one taxonomy slug
//	?parent_id=<uuid>         — direct children of a parent (or empty for top level)
//	?search=<prefix>          — name prefix; case-insensitive
//
// The response surfaces the materialized ltree path so a frontend
// can build a breadcrumb without a follow-up query. Depth is
// computed (nlevel(path)) so clients don't have to parse the ltree.
package terms
