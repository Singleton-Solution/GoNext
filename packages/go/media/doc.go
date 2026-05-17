// Package media is the GoNext media-pipeline primitives package.
//
// The full media pipeline — libvips-backed variant generation, S3 storage,
// the variants table, and the /media/{id} HTTP proxy — lives in separate
// issues. This package ships the in-process building blocks that those
// pieces share. Today that's one piece: the single-flight Coalescer.
//
// # Coalescer
//
// When N concurrent requests ask for the same missing media variant
// (e.g., /media/{id}?w=800&h=600), only ONE of them should actually run
// the expensive libvips pipeline; the others should attach to the same
// in-flight future and receive the same bytes. This is "single-flight"
// — also called "request coalescing" or "stampede protection" — and is
// the standard defense against the thundering-herd CPU/DB spike that
// occurs when a hot URL goes viral on a cold cache.
//
// Wiring is intentionally narrow:
//
//	c := media.NewCoalescer(media.CoalescerOptions{
//	    Counter: prometheusCounter, // implements media.Counter
//	    Logger:  slog.Default(),
//	})
//
//	// In the variant handler, after a cache miss:
//	bytes, shared, err := c.Get(ctx, variantKey, func() ([]byte, error) {
//	    return libvips.Render(spec) // the actual generation
//	})
//	if err != nil { ... }
//	if shared { /* this caller waited for someone else's render */ }
//
// The Coalescer is per-process, NOT cross-node — see
// docs/07-media-performance.md §5.3a for the cross-node defense (CDN
// coalescing at the edge plus ON CONFLICT DO NOTHING on the variants
// table for the rare miss-on-different-nodes case).
//
// # Key canonicalization
//
// Two query strings can describe the same variant: w=800&h=600&fit=cover
// and h=600&fit=cover&w=800. Without canonicalization those would each
// start their own render. CoalescerOptions.KeyExtractor lets the caller
// supply a function that maps the raw key to a canonical form before
// the singleflight lookup; SortedQueryKey is shipped as a ready-to-use
// canonicalizer for query-string-shaped keys.
//
// # Metrics
//
// The Counter interface is intentionally minimal — one Inc(name) method
// — so callers can wire it to Prometheus, OpenTelemetry, or a test
// double without dragging a transitive dep into this package. Two
// counters are emitted:
//
//   - media_variant_coalesce_total: incremented for each follower (a
//     caller that attached to someone else's in-flight render). This is
//     the metric to graph when proving "did the single-flight pool
//     actually save us work?".
//
//   - media_variant_generate_total: incremented for each leader (a
//     caller that actually ran generate). Sum of generates plus
//     coalesces equals total Get calls; the ratio of coalesces to total
//     is the stampede-absorption rate.
//
// Stats() additionally exposes an in-process snapshot
// {InFlight, TotalCoalesced, TotalGenerated} that's useful for /debug
// endpoints and for tests that need to assert on internal counters
// without scraping Prometheus.
//
// See docs/07-media-performance.md §5.3a for the surrounding design.
package media
