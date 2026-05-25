// Package img is the public HTTP handler for the on-the-fly image
// proxy route at GET /img/{id}/{spec}.
//
// # Wire shape
//
// Each request decomposes into two path segments — the media row's
// UUID and the variant spec — and is served from a local disk cache
// whose entries are produced lazily by the imgproxy.Transformer.
//
//	GET /img/3c8e2f7a-...-90/w-800.h-600.q-85.fit-cover.webp
//
//	Response: 200 OK
//	  Content-Type: image/webp
//	  Cache-Control: public, max-age=31536000, immutable
//	  Vary: Accept
//	  Content-Length: <bytes>
//	  <encoded image bytes>
//
// Errors:
//
//   - 400 Bad Request — spec failed to parse (unknown token,
//     out-of-range value, missing dimension).
//   - 404 Not Found — no media row matches the ID, or the source
//     bytes are absent from the storage backend.
//   - 415 Unsupported Media Type — the source bytes decode-failed.
//   - 504 Gateway Timeout — context cancelled mid-transform (caller
//     disconnected or upstream storage call timed out).
//   - 500 Internal Server Error — transformer or cache I/O failure.
//
// # Concurrency
//
// Concurrent requests for the same (id, spec) collapse to a single
// transform via packages/go/media.Coalescer. The first request
// invokes the transformer; subsequent requests block on the
// coalescer until the leader finishes and reuse the leader's
// rendered bytes. Cache writes go through the coalescer's leader so
// only one writer ever touches a given cache file at a time.
//
// # Cache invalidation
//
// The cache is content-addressed by (id, canonical-spec); a row
// update that produces new source bytes also produces a new media
// ID, so old cache entries become orphaned but not stale. A future
// admin "purge cache" surface can rm -rf the cache root without
// disturbing the upload pipeline.
package img
