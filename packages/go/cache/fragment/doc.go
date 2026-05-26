// Package fragment is a Redis-backed byte-fragment cache with
// tag-based invalidation that piggy-backs on the cache_invalidations
// outbox shipped by migration 000030.
//
// Why a fragment cache (vs. the existing KV ABI)
//
// The plugin-facing KV ABI in packages/go/plugins/runtime/host_data.go
// is namespaced per plugin, quota-tracked, and audit-emitted on every
// write. Those properties are right for plugin storage but wrong for
// internal render memoisation: the block render walker (#108) wants
// a single shared cache pool keyed by content hash, no quotas, and no
// audit row per hit. Fragment is that pool.
//
// Why tags instead of key fanout
//
// A typical render reads from many keys ("block:hero:v3", "menu:main",
// "site-settings:colors"). Invalidating "all renders that touched the
// main menu" by enumerating every dependent key would force the
// producer side to remember an N:M reverse index. Tags collapse that
// into a single PURGE TAG ('menu') message: every fragment indexes its
// own tag set on write, and the worker bumps a per-tag version on
// invalidate. A fragment is fresh iff every one of its tag versions
// still matches the values it captured at write time.
//
// # Storage layout
//
// Redis keys are namespaced under "gnf:" to avoid colliding with the
// plugin KV pool (which uses "plugin:<slug>:").
//
//	gnf:f:<key>         -> the cached payload (bytes) + the captured
//	                       tag version vector encoded as the first
//	                       few bytes of the value (see encodeEntry).
//	gnf:tv:<tag>        -> int64, monotonically incremented each time
//	                       the tag is invalidated. Lazily created on
//	                       first read with a value of 0.
//
// The version-vector approach is the same one used by Mnesia /
// transactional caches: cheap reads (one MGET against the tag-version
// keys plus one GET on the payload), invalidation is O(1) (a single
// INCR per tag), and there's no key-fanout problem on the producer
// side.
//
// # Invalidation flow
//
//	Set(ctx, key, value, tags, ttl)
//	  → MGET each tv:<tag> (lazy-creates them at 0)
//	  → encode (versions, value) into a single Redis value
//	  → SET gnf:f:<key> EX ttl
//
//	Get(ctx, key, tags)
//	  → GET gnf:f:<key>
//	  → decode (capturedVersions, value)
//	  → MGET each current tv:<tag>
//	  → if every captured == current, return (value, true, nil)
//	  → otherwise return (nil, false, nil) — the cache itself never
//	    deletes; the next Set overwrites with a fresh capture, and
//	    Redis' EX handles eviction.
//
//	Purge(ctx, tags)
//	  → For each tag, write a row into cache_invalidations. The
//	    invalidator worker (packages/go/cache/invalidator) drains
//	    that table, INCRs gnf:tv:<tag>, and publishes a pub/sub
//	    message. After the worker drains, any in-flight Get whose
//	    captured version disagrees returns a miss.
//
// Tags written to the outbox are stored UNPREFIXED by the worker
// convention (see invalidator.go). The fragment cache writes its
// own internal "gnf" prefix on the pub/sub subscriber side so the
// version keys it manages don't collide with the plugin-KV namespace.
//
// # Concurrency and consistency
//
// Get is a single round-trip in the steady state (payload GET) and a
// second round-trip on a hit candidate (MGET tag versions). Stale
// reads are bounded by the outbox poll cadence (default ~100ms) plus
// the time it takes the worker to PUBLISH and INCR — typically under
// 200ms for a single invalidation. The cache is designed for content
// that tolerates that window (block renders, sitemap fragments,
// menu HTML); it is NOT a source-of-truth store.
//
// # Why no Delete
//
// Direct Delete(key) is intentionally absent. The tag mechanism is
// the only invalidation surface so authors don't end up with two
// parallel paths to remember. Code that wants to drop a single
// fragment uses a tag whose only fragment is that one ("block:<id>").
package fragment
