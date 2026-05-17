-- 000014_idempotency.up.sql
--
-- Durable backing store for the Idempotency-Key middleware
-- (packages/go/jobs/idempotency/). The hot-path lookup goes through
-- Redis (see store.go:RedisStore); this table is the slower, durable
-- tier that survives a Redis flush, gives operators an audit trail of
-- every replayed request, and lets us re-hydrate the cache after a
-- cold start.
--
-- Two-tier semantics, mirroring the rest of the codebase
-- (sessions, ratelimit):
--
--   Redis  →  fast hit-or-miss for the in-progress / succeeded claim.
--             SETNX-style atomic via a Lua script (store.go).
--   Postgres → durable record of the request hash and stored result.
--             Wins on a Redis miss, lets us replay a successful
--             response weeks later for audit purposes.
--
-- The Go contract lives in packages/go/jobs/idempotency/ and was
-- locked by issue #264.
--
-- Depends on:
--   * 000001_init — gen_uuid_v7() is not needed here (the key is
--                   the client-supplied header value), but the
--                   extension chain that 000001 installs IS — JSONB
--                   and the BYTEA codec come from pgcrypto + builtin.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE idempotency_keys (
    -- The client-supplied Idempotency-Key header value. PK so the
    -- middleware's "look up by key" is a single B-tree probe. We do
    -- NOT hash this — the header is already opaque from our point
    -- of view, and exposing the plaintext key in queries makes
    -- operator debugging ("why does this client keep replaying?")
    -- tractable.
    --
    -- Length-capped at 255 — the spec
    -- (https://www.ietf.org/archive/id/draft-ietf-httpapi-idempotency-key-header-06.html#section-2.2)
    -- recommends ≤ 255 chars; we enforce it at the Go layer too
    -- (key.go:Validate) but the column cap is the load-bearing
    -- guarantee.
    key             TEXT PRIMARY KEY
                    CHECK (length(key) > 0 AND length(key) <= 255),

    -- SHA-256 of the canonicalised request (method + path + body).
    -- When a client replays the same key with a DIFFERENT body, the
    -- middleware returns 422 instead of a stale cached response —
    -- this hash is what powers that check. 32 bytes of binary; we
    -- store as BYTEA not hex because the round-trip is on every
    -- replay.
    request_hash    BYTEA NOT NULL
                    CHECK (octet_length(request_hash) = 32),

    -- in_progress | succeeded | failed.
    --
    --   in_progress: the original handler is still running. Concurrent
    --                replays of the same key get 409.
    --   succeeded:   handler returned a 2xx; result_code + result_body
    --                hold the snapshot for replay.
    --   failed:      handler returned a non-2xx. We still record the
    --                outcome so a retry sees the same failure rather
    --                than re-running a possibly-expensive operation
    --                that just turned out to violate a business rule.
    --
    -- TEXT + CHECK rather than an ENUM so the set is editable without
    -- a migration if we ever need a fourth state (e.g. "cancelled").
    -- See the matching IdempotencyStatus constants in key.go.
    status          TEXT NOT NULL
                    CHECK (status IN ('in_progress', 'succeeded', 'failed')),

    -- Stored HTTP status code from the original response. NULL while
    -- status='in_progress'; populated once the handler returns.
    result_code     INT,

    -- Stored response body, kept as JSONB so the prune query can
    -- TOAST-prune large bodies cheaply and so operators can SELECT
    -- result_body->'error_code' for triage. NULL while
    -- status='in_progress'.
    --
    -- Bodies that aren't valid JSON (rare for an API server, but
    -- possible for e.g. file downloads) are wrapped in
    -- {"_raw_base64": "..."} by the Go layer — see store.go.
    result_body     JSONB,

    -- When the claim was first opened. Used for the audit trail and
    -- to bound the prune query's range scan.
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- When the row becomes eligible for pruning. The middleware sets
    -- this to created_at + ttl on every claim (default 24h, see
    -- store.go:DefaultTTL). The scheduled prune (issue #264 §7)
    -- deletes any row where expires_at < now().
    expires_at      TIMESTAMPTZ NOT NULL,

    -- Defence in depth: a row in 'in_progress' MUST have NULL result
    -- fields, and a row in a terminal state MUST have them populated.
    -- Catching this at the DB layer means a buggy Go path can't store
    -- "succeeded but no body" silently.
    CONSTRAINT idempotency_keys_terminal_fields_chk
        CHECK (
            (status = 'in_progress' AND result_code IS NULL AND result_body IS NULL)
            OR
            (status IN ('succeeded', 'failed') AND result_code IS NOT NULL)
        )
);

-- =============================================================================
-- Indexes
-- =============================================================================

-- The scheduled prune (idempotency.PrunePostgres in store.go) deletes
-- rows where expires_at < now(). Without this index that's a sequential
-- scan over the entire history of keys.
CREATE INDEX idempotency_keys_expires_at_idx
    ON idempotency_keys (expires_at);
