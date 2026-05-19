// Package search wires the admin-facing site-search REST endpoint.
//
// Route: GET /api/v1/admin/search?q=&types=post,page
//
// Surface
// =======
//
// The admin endpoint differs from the public /api/v1/search in three
// ways that justify the separate package:
//
//  1. It requires an authenticated principal (any logged-in user is
//     allowed — there is no narrower cap, because every admin page
//     needs to surface relevant content regardless of the user's
//     edit privileges). The public endpoint is anonymous.
//
//  2. It does NOT pin Status to "published". Admins want to find
//     their drafts, scheduled posts, and private rows alongside the
//     published ones; the search.Store handles that naturally when
//     SearchOpts.Status is empty.
//
//  3. It is NOT IP-rate-limited. The admin surface is already
//     session-rate-limited via the auth middleware; piling an extra
//     bucket on top would mainly hit operators rapidly cmd+k-ing
//     through results.
//
// Everything else — the underlying SQL, the highlighter, the result
// shape — is shared with the public handler via the search package.
package search
