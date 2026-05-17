// Package posts implements the REST CRUD surface for the posts table —
// both the `post` and `page` post types, mounted under
// /api/v1/posts and /api/v1/pages respectively. Both surfaces share
// the same handler set; the post_type discriminator decides whether
// requests at a given mount point read/write `post` rows or `page` rows.
//
// # Endpoints
//
// For each mount the package exposes:
//
//	GET    /api/v1/{posts|pages}          — list with filters + cursor pagination
//	GET    /api/v1/{posts|pages}/{id}     — single resource with conditional GET
//	POST   /api/v1/{posts|pages}          — create
//	PATCH  /api/v1/{posts|pages}/{id}     — update with If-Match version check
//	DELETE /api/v1/{posts|pages}/{id}     — trash with If-Match version check
//
// # Capabilities
//
// Route-level gates use the primitive capabilities from packages/go/policy.
// Object-level gates (author can edit own; non-author needs edit_others)
// run inside the handler after the row is loaded so the policy decision
// can see the resource's author_id.
//
// The mapping for the `post` mount:
//
//	GET list  /  GET one : no cap (public posts) — non-public statuses
//	                       require capability access (read_private_posts).
//	POST              : edit_posts (route-level gate)
//	PATCH (own row)   : edit_posts (object-level: principal must be author)
//	PATCH (other row) : edit_others_posts
//	DELETE (own)      : delete_posts
//	DELETE (other)    : delete_others_posts
//
// The `page` mount uses the corresponding *_pages capabilities. The
// type discriminator is set in [Deps.PostType] at Mount time.
//
// # Optimistic concurrency
//
// Every PATCH and DELETE require an If-Match header carrying the
// caller's last-known version. The handler matches it against the row's
// current version inside the UPDATE WHERE clause (the bump_version
// trigger guarantees monotonicity); a mismatch returns 412 with
// problem code "version_mismatch".
//
// GET responses carry two cooperating headers:
//
//   - ETag: a strong hex-encoded form of content_blocks_hash. Used by
//     clients for conditional GET (If-None-Match -> 304).
//   - X-Version: the integer version. Clients echo it back in If-Match
//     on writes. We could overload the ETag for both roles but the two
//     concerns evolve independently (content hash changes only when
//     blocks change; version changes on every UPDATE) and keeping them
//     in separate headers reads more clearly in logs.
//
// # Password-protected posts
//
// A post with a non-NULL `password` column is protected: the GET-one
// response strips content_blocks / content_rendered and surfaces only
// the metadata unless the caller proves they hold the password via the
// `X-Post-Password` request header. The actual password hashing scheme
// is owned by a future auth-layer issue; this package treats the column
// as opaque — protected status is "non-empty password" and the password
// match is a byte-exact comparison against the supplied header. The
// header-vs-column scheme will swap out for argon2id verification when
// the auth layer lands; the handler-visible contract (protected → blank
// content) stays.
//
// # Content blocks validation
//
// content_blocks is a JSONB array of block descriptors per ADR 0008.
// At this issue's scope we validate only structurally: the field must
// be a JSON array, each entry an object, each entry having a non-empty
// `type` string. A full block-schema validator is deferred to the
// block-registry issue; this package's validation is the minimum that
// prevents a malformed row from being persisted.
package posts
