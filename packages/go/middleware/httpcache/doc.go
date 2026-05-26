// Package httpcache provides a small, per-route opt-in middleware that
// emits caching headers (ETag, Last-Modified, Vary, Cache-Control) on
// safe responses (GET, HEAD), and serves 304 Not Modified when a client
// echoes a matching ETag back via If-None-Match.
//
// # Why opt-in (not blanket)
//
// Most routes in the chassis fall into one of three buckets:
//
//  1. Authenticated mutation endpoints (POST/PUT/PATCH/DELETE) — must
//     NOT be cached. These set `Cache-Control: no-store` explicitly
//     (see packages/go/middleware/auth's writeJSONError for the 401
//     case).
//
//  2. Authenticated read endpoints with per-user data (the admin REST
//     surface). Caching these would leak one user's view to another.
//     The middleware refuses to set ETag/Vary on responses that have
//     already set `Cache-Control: private` or `no-store` upstream.
//
//  3. Public read endpoints (the public-site renderer's JSON feeds,
//     sitemap, theme assets resolved through the API). These benefit
//     enormously from ETag + CDN revalidation. They opt into this
//     middleware explicitly via Mount.
//
// A blanket middleware that ETag'd every response would catch (3) but
// also (2) — leaking session-scoped data through a CDN. The per-route
// opt-in is the safe default.
//
// # What it does
//
// For safe-method (GET/HEAD) responses:
//
//   - Buffers the response body in memory until the handler returns,
//     then computes a SHA-256 over the buffered bytes (truncated to 16
//     bytes hex == 32 chars in the ETag value, sufficient for
//     collision-free identification of an HTTP body).
//   - Sets `ETag: "<hash>"` and (if Vary headers were supplied at
//     construction time) `Vary: <h1>, <h2>, ...`.
//   - If the request carries If-None-Match and any of the comma-
//     separated values match the computed ETag, the buffered body is
//     discarded and the response is rewritten to 304 Not Modified with
//     the ETag header retained.
//
// For unsafe methods (POST/PUT/PATCH/DELETE), the middleware is a
// transparent pass-through — it does not allocate the buffering layer
// at all.
//
// # Limitations
//
// The buffering strategy means streaming endpoints (Server-Sent
// Events, chunked downloads) MUST NOT be wrapped — they would be
// fully materialized in memory. The middleware has no way to detect
// this automatically (Content-Type is set late, often after the first
// Write), so the contract is "wrap only routes whose body is bounded
// and small". The public-site JSON feeds are well-bounded; sitemap.xml
// is bounded by the site's post count. Anything larger should serve
// from object storage with the CDN handling cache headers natively.
//
// We also do NOT touch the Cache-Control header. Setting it correctly
// is route-specific (some public reads should be `public, max-age=60`,
// others `no-cache` to force revalidation every time). The caller
// passes that via Options.CacheControl when they want it.
package httpcache
