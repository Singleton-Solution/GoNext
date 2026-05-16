# ADR 0011: Cache invalidation is tag-based and uses a transactional outbox

- **Status**: proposed
- **Date**: 2026-05-17
- **Deciders**: @tayebmokni
- **Consulted**: doc 07 §15–16 (caching layers and invalidation), proposal Q12-5
- **Informed**: API authors, plugin authors, ops

## Context

GoNext caches at every layer that matters: HTTP cache at the CDN, Next.js ISR for full-page caches, fragment cache in Go/Redis, object cache in Go/Redis, and a block-render cache. Doc 07 §15 enumerates them. Aggressive caching is the only way to hit the latency budget for a CMS-shaped workload, and the public site (ADR 0007) depends on it.

The hard problem in any layered cache is **invalidation**. When a mutation lands in Postgres — a post gets published, a category gets renamed, a navigation menu gets reordered — the cached representations of that data across all five layers have to be evicted. Get it wrong and editors see stale pages, readers see deleted content, or worse, an authenticated user's response leaks into the public cache.

Three rough approaches:

1. **TTL-only.** Every cached entry has an expiry; the cache becomes consistent eventually. Simple, but the staleness window is too long for editor workflows — when an editor clicks "publish," they expect the page to update now, not in 60 seconds.
2. **In-request synchronous invalidation.** Every mutation calls every cache layer's invalidator inline before returning to the client. Couples mutation latency to invalidation completion. A "publish" that has to wait for the CDN purge API to round-trip is unacceptable. If any one layer is slow or down, the mutation slows or fails.
3. **Tag-based invalidation via transactional outbox.** Every cached entry carries one or more tags. Every mutation writes an `INSERT INTO cache_invalidations (tags, ...)` row inside the same transaction as the mutation. A dedicated worker drains the outbox and fans out invalidations to every cache layer. Mutation latency stays bounded; invalidation is asynchronous but durable.

Doc 07 §16 picks (3) for the reasons laid out below, plus a backstop TTL on every cached entry for the rare case where the worker is far behind or a row's invalidation is lost.

The canonical tag vocabulary (doc 07 §16.1 — contract S5) is dotted lowercase with UUID v7 (ADR 0003) where the tag references an entity:

- `post:{uuid}` — a specific post
- `term:{uuid}` — a taxonomy term
- `user:{uuid}` — a user
- `type:{slug}` — all content of a post type
- `archive:{type}:{taxonomy}:{term-uuid}` — a taxonomy-scoped archive
- `media:{uuid}` — a media item
- `nav:{uuid}` — a navigation menu
- `site:settings` — site-wide settings
- `global` — nuclear option

Plugins (ADR 0005, ADR 0012) get a `cache.invalidate(tags)` host ABI call gated by the `cache.invalidate` capability. The host validates tags against the registry — unknown tags are rejected. The ABI call writes into the same `cache_invalidations` outbox inside the plugin's current transaction, so plugin mutations and their cache effects roll back together. Plugins cannot write to the outbox by direct SQL (they do not have `db.write` on core tables); the ABI is the only path.

## Decision

Cache invalidation is **tag-based, written to a `cache_invalidations` outbox table in the same Postgres transaction as the mutation that triggered it**, drained asynchronously by an `invalidation-worker` that fans out to every cache layer (fragment cache, Next.js ISR via `/api/revalidate`, CDN purge via cache-tag API). The canonical tag vocabulary is doc 07 §16.1. Plugins invalidate via the `cache.invalidate` host ABI (ADR 0012), which translates to outbox rows. Every cached entry also carries a backstop TTL so a worst-case outbox failure cannot leave content stale indefinitely.

## Consequences

### Positive

- **Mutation latency is decoupled from invalidation latency.** A `POST /posts/:id/publish` returns as soon as Postgres commits. The cache fan-out happens behind the scenes. Even if the CDN purge API is slow, the publish is fast.
- **Atomicity.** If the mutation rolls back, the outbox row rolls back too. There is no "we invalidated the cache but the write failed" inconsistency. This is the killer feature versus in-request synchronous invalidation.
- **Durability.** If the worker crashes mid-drain, the outbox row remains. On restart, it picks up where it left off. The worst-case failure is delayed invalidation, never lost invalidation. Single-row failures are retried with exponential backoff.
- **Forensics.** The `cache_invalidations` table is a permanent record of "what got invalidated when, triggered by whom." When a stale-cache bug ships, we replay the timeline.
- **Plugin contract is honest.** Plugins do not call Next.js or the CDN directly. They call one host ABI; the host owns the fan-out. Plugin updates do not break when we swap CDN providers.
- **Multi-layer coordination is the worker's problem.** Adding a new cache layer (say, an edge KV cache) means teaching the worker to fan out to it. No change to mutation code.

### Negative

- **Eventual consistency.** Between commit and worker drain there is a short window (target sub-second) where caches still serve old data. For editor workflows this is acceptable; for systems that need strict read-after-write across cache layers, this would not be acceptable. We accept the tradeoff.
- **One more worker to run.** The `invalidation-worker` is a small Go process that joins the worker fleet (Asynq for general jobs, ADR 0010, plus this dedicated outbox drainer). Operationally it is one more thing to monitor.
- **Outbox table grows.** Every mutation writes a row. The worker deletes drained rows, so steady-state size is bounded by drain rate × write rate. We monitor and alert if the drain falls behind.
- **Tag explosion is a real failure mode.** A mutation that touches a post invalidates `post:{id}` plus every term tag plus possibly `post-list:global`. A bulk operation that touches 10,000 posts produces 10,000+ invalidation rows. Mitigation: bulk operations write coarse tags (`type:post`) instead of per-entity tags; the worker collapses duplicate tags in its drain.
- **Backstop TTL is real.** Even with the outbox, every cached entry has a TTL so a worst-case outbox failure leaves a bounded staleness window. We do not rely on the TTL for correctness in the normal case.

### Neutral / accepted tradeoffs

- The tag vocabulary is canonical and tight. Plugins cannot mint new tag namespaces (the host validates against the registry). Adding a new tag is a core change. This is intentional — open-ended tag vocabularies degrade into "everything tagged with `everything`."
- We do not use Redis pub/sub for cache invalidation. Pub/sub is fire-and-forget; the outbox is durable. Doc 07 §16.2 picks durability over latency for invalidation.
- The same outbox handles core mutations, renderer cache regeneration, and plugin invalidations. Three classes of producers, one shape (doc 07 §16.2).

## Alternatives considered

### Option A: In-request synchronous invalidation
- Rejected. Couples mutation latency to invalidation completion. A "publish" that has to wait for the CDN purge API to return is unacceptable. Any layer being slow or down slows or breaks the mutation. Loss of atomicity: if invalidation runs before commit and commit fails, the cache is wrong; if it runs after commit, a crash between the two leaves stale data.

### Option B: TTL-only invalidation
- Rejected. The staleness window is too long for editor workflows. Editors expect "publish" to take effect immediately. A 60-second TTL means an editor sees their own draft instead of the published page for up to a minute after publishing. Documented WordPress complaint pattern.

### Option C: Redis pub/sub for invalidation events
- Rejected. Pub/sub is fire-and-forget; messages dropped during a Redis blip are lost. We need durable invalidation. The outbox is durable; pub/sub is not.

### Option D: Per-layer invalidation APIs called from the application
- Rejected. Forces every mutation site to know about every cache layer. Adding a new layer (or a new cache) requires touching every mutation. The outbox-plus-worker pattern lets us add layers without touching mutation code.

### Option E: Postgres `LISTEN/NOTIFY`-driven invalidation
- Considered. Postgres-native, no extra table, no polling. Rejected because NOTIFY payloads are bounded (8KB) and lost on connection loss; we lose durability. The outbox is durable across worker restarts.

### Option F: Tag-less, key-pattern invalidation (e.g., `DEL cache:post:*`)
- Rejected. Scan-based invalidation on Redis is slow and unsafe (KEYS is blocking; SCAN-then-DEL is racy under concurrent writes). Per-layer key conventions also fragment quickly. Tags are layer-agnostic.

## References

- Design doc: `docs/07-media-performance.md` §15 (caching layers), §16 (cache invalidation strategy)
- Design doc: `docs/07-media-performance.md` §16.1 (canonical tag vocabulary), §16.2 (transactional outbox)
- Design doc: `docs/02-plugin-system.md` §6.6 (plugin `cache.invalidate` capability)
- Proposal: `docs/proposals/14-proposals-ops-sec.md` Q12-5
- Related ADRs: ADR 0003 (UUID v7 used in tag values), ADR 0004 (Postgres for the outbox), ADR 0010 (Asynq for other jobs — invalidation worker is separate), ADR 0012 (capability gates plugin invalidations)
