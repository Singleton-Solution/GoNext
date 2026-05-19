// Package search wires the public, anonymous site-search REST
// endpoint.
//
// Route: GET /api/v1/search?q=<term>
//
// Contract
// ========
//
// The public endpoint is the data layer behind the front-end theme's
// search.html template (docs/03-theme-system.md §4.2 — "search"
// candidate). Themes drive the route either by JS fetch
// (progressive enhancement) or by server-rendering inside the
// renderer's search handler, which calls this package's Searcher
// surface directly without going over HTTP.
//
// Differences from /api/v1/admin/search:
//
//  1. Anonymous — no principal is required. The endpoint is mounted
//     OUTSIDE the auth middleware on the public router.
//
//  2. Status is forcibly pinned to "published". Drafts, scheduled,
//     and private posts are NEVER reachable through this endpoint
//     regardless of what the client sends in the URL. The pin
//     happens server-side; ignoring a client-supplied status is
//     intentional (an anonymous caller has no business setting it).
//
//  3. Rate-limited per-IP via packages/go/ratelimit. The configured
//     bucket is small (a handful of requests per second per IP):
//     the search endpoint is the cheapest DoS vector in the public
//     API surface.
//
//  4. Total counts are skipped by default (SkipTotal=true). The
//     public-search template renders an infinite-scroll list, not
//     a paginated "1 of 137" footer. Skipping the COUNT halves the
//     per-request cost. Clients that need the total can pass
//     ?total=1 (the parser honors it).
package search
