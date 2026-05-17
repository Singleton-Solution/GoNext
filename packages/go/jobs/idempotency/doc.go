// Package idempotency implements the Idempotency-Key pattern for HTTP
// operations that MUST NOT be replayed on a client retry — creating a
// payment, sending a one-time email, enqueueing an outbound webhook.
//
// The contract follows the IETF draft
// (draft-ietf-httpapi-idempotency-key-header-06): a client picks a
// random Idempotency-Key, includes it on the request, and gets one of
// three outcomes:
//
//   - First time we've seen this key → run the handler, store the
//     result, return it. Subsequent replays with the same body return
//     the stored snapshot, not duplicate work.
//
//   - Same key, same request → replay the stored response. The handler
//     is NOT called.
//
//   - Same key, DIFFERENT request → 422 idempotency_key_reused. This
//     catches the easy mistake of reusing a key for two different
//     operations and refusing to silently overwrite the prior result.
//
//   - Same key, in-flight → 409 idempotency_key_pending. Concurrent
//     replays don't double-execute; only one wins and the other waits
//     by retrying client-side.
//
// # Two-tier store
//
// The hot path goes through Redis: a single Lua-atomic claim returns
// "new", "exists with same hash", "exists with different hash", or
// "in progress". Sub-millisecond on the happy path.
//
// Postgres is the durable tier: every successful claim writes through
// to idempotency_keys (migration 000014). This survives a Redis flush,
// gives operators an audit trail of every replayed key, and lets the
// cache rebuild after a cold start. The Store interface composes the
// two — see [Store] and [TieredStore].
//
// # TTL
//
// Both tiers expire entries after [DefaultTTL] (24h) by default. Redis
// uses native EX/PX expiry; Postgres rows carry an expires_at column
// and rely on a scheduled prune call ([PostgresStore.Prune]) to reclaim
// space.
//
// # Middleware
//
// [Middleware] wires the store into an HTTP handler. It hashes the
// canonical request (method + path + body) with SHA-256, claims via
// the store, and either dispatches to the inner handler or replays the
// stored response. See middleware.go for the full state machine.
package idempotency
