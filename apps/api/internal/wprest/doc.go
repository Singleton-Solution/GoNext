// Package wprest implements the WordPress REST API v2 compatibility shim.
//
// The shim translates the GoNext-native REST surface (apps/api/internal/rest)
// into the WordPress v2 envelope at /wp-json/wp/v2/...  so that existing
// clients (mobile apps, headless frontends, third-party integrations, build
// pipelines) keep working unchanged after a site migrates from WordPress to
// GoNext. See docs/08-migration-compat.md §11 for the full background.
//
// # Scope of this issue (#89)
//
// This PR is the read-only baseline. Write methods (POST/PUT/DELETE) return
// 405 Method Not Allowed with a WP-shaped error body. Migration cohort tests
// only exercise reads at this stage; write semantics carry an entirely
// different correctness surface (auth, ownership, validation, nonce handling)
// that we sequence behind the read shim landing.
//
// # Endpoints
//
// The following collections are registered:
//
//	GET /wp-json/wp/v2/posts          — collection listing
//	GET /wp-json/wp/v2/posts/{id}     — single resource by legacy_int_id
//	GET /wp-json/wp/v2/pages          — pages collection
//	GET /wp-json/wp/v2/pages/{id}     — single page
//	GET /wp-json/wp/v2/users          — users (sensitive fields gated)
//	GET /wp-json/wp/v2/users/{id}     — single user
//	GET /wp-json/wp/v2/categories     — taxonomy terms (category)
//	GET /wp-json/wp/v2/categories/{id}
//	GET /wp-json/wp/v2/tags           — taxonomy terms (post_tag)
//	GET /wp-json/wp/v2/tags/{id}
//
// # Field shape
//
// Each translator emits the WP envelope as documented in 08 §11.2. Notably:
//
//   - integer `id` (the post's legacy_int_id, stable per row)
//   - `slug`, `link`, `status`, `type`
//   - `title` is an object with `rendered` (and `raw` for authenticated
//     callers — out of scope here; we always emit rendered)
//   - `content` and `excerpt` are objects with `rendered` (and `protected`)
//   - `_links` is the HAL-style discovery block
//   - `_embedded` is populated only when the request carries `?_embed`
//
// # Query parameters
//
// WP query params honored on collection routes:
//
//	page         — 1-indexed page number, default 1
//	per_page     — 1..100, default 10 (WP default)
//	search       — free-text title search
//	categories   — comma-separated legacy_int_ids (or repeated ?categories[])
//	tags         — comma-separated legacy_int_ids (or repeated ?tags[])
//	orderby      — date|title|id|slug (default date)
//	order        — asc|desc (default desc)
//	_embed       — when present, populate _embedded on responses
//
// Response headers `X-WP-Total` and `X-WP-TotalPages` are set on every
// collection response. Clients (notably JS WP SDKs) depend on them for
// paging.
//
// # Errors
//
// Errors follow the WP shape: `{code, message, data:{status, ...}}`. See
// errors.go for the canonical writer. The pre-existing GoNext RFC 7807
// problem details body would confuse WP clients, so the shim layer DOES
// NOT use packages/go/policy/router.WriteError directly.
package wprest
