// Package wprest implements the WordPress REST API v2 compatibility shim.
//
// The shim translates the GoNext-native REST surface (apps/api/internal/rest)
// into the WordPress v2 envelope at /wp-json/wp/v2/...  so that existing
// clients (mobile apps, headless frontends, third-party integrations, build
// pipelines) keep working unchanged after a site migrates from WordPress to
// GoNext. See docs/08-migration-compat.md §11 for the full background.
//
// # Scope
//
// Issue #89 landed the read-only baseline. Issue #227 extends the shim with
// write methods (POST/PUT/PATCH/DELETE) for posts, pages, users, categories,
// and tags. The write surface is opt-in: when the corresponding *Sink in
// Deps is nil the shim returns the same 405 Method Not Allowed with a
// WP-shaped error body as before; when the sink is wired, the handler runs
// the full write pipeline (nonce → principal → capability → decode →
// resolve terms → sink → audit → response).
//
// # Write semantics
//
// Authentication is via the existing session cookie (the auth middleware
// stashes a policy.Principal on the request context). Cross-site request
// forgery protection is via the existing CSRF middleware: the shim's
// X-WP-Nonce header is a WP-flavored alias for the CSRF token, with the
// `?_wpnonce=` query parameter as a fallback for clients that can't set
// headers. The bridge is in auth.go.
//
// Capability checks use packages/go/policy:
//
//	POST   /posts        → CapEditPosts
//	PUT    /posts/{id}   → CapEditPosts
//	DELETE /posts/{id}   → CapDeletePosts
//	POST   /pages        → CapEditPages
//	(same pattern for pages / users / categories / tags)
//
// Object-level checks (edit_others_posts, delete_others_posts) live in
// the sink layer where the resource has been loaded.
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
