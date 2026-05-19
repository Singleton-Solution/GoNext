// Package redirects implements explicit URL redirect rules administered
// through the admin UI. It sits in front of the renderer in the HTTP
// middleware chain so a matched rule short-circuits with a 301/302/
// 307/308 before the renderer wastes a database round-trip on a path
// that no longer maps to content.
//
// The package is split into three concerns:
//
//   - Store    — durable persistence (PgxStore on Postgres,
//     InMemoryStore for tests). The Store is the source of truth; the
//     engine snapshots from it at boot.
//
//   - Engine   — in-memory rule index. Literal rules go into a hashmap
//     (O(1) lookup); regex rules are pre-compiled and iterated in
//     creation order (first match wins). The engine owns the hit
//     counter; it batches counter writes to the Store every 30s so the
//     hot path stays lock-free.
//
//   - Middleware — HTTP wrapper. On each request, asks the engine to
//     match; on hit, writes the redirect response. The middleware
//     also implements loop protection: a request that traverses more
//     than 5 redirect hops on the same in-process chain gets a 508
//     Loop Detected.
//
// Distinct from permalinks (which point at live content rows), this
// table is operator-curated and outlives the content it originally
// referenced. WordPress sites accumulate years of these "moved
// permalink" rules; we need parity.
package redirects
