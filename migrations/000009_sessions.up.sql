-- 000009_sessions.up.sql
--
-- `sessions` — Postgres mirror of the Redis-backed session store.
--
-- The session store landed in PR #291 and is Redis-first: every hot-
-- path lookup (`session:<sid_hash>`) goes through Redis, never
-- Postgres. Redis is the source of truth for *active* sessions.
--
-- This migration adds an **optional Postgres mirror** for the two
-- use-cases Redis alone can't serve well (doc 06 §5.3):
--
--   1. "Where you're logged in" — the `/me/sessions` page needs to
--      enumerate every active session for a user. Doing this against
--      Redis means a `SCAN` over the entire keyspace; that's fine for
--      a single-tenant test box and pathological for production. A
--      Postgres index on `(user_id, last_seen_at DESC)` answers the
--      same query in milliseconds and survives a Redis flush.
--
--   2. Audit trail + multi-replica recovery. If Redis goes down we
--      lose every active session, which means every user gets
--      logged out simultaneously. The mirror lets the auth path fall
--      back to Postgres on a Redis miss (gated by a flag — see doc
--      06 §5) and gives the audit log a durable record of every
--      session that ever existed, including revoked ones.
--
-- This migration depends on:
--   * 000001_init   — `pgcrypto`, `gen_uuid_v7()`.
--   * 000002_users  — `users(id)` for the FK.
--
-- Notes on what is **not** here:
--
--   * No `csrf_token`. The CSRF token lives in the Redis session blob
--     (doc 06 §11). Mirroring it would mean storing the token plain-
--     text in Postgres, which we explicitly don't do.
--   * No `factors` array. That's a per-Redis-blob concern; the mirror
--     only needs enough state to render the "where you're logged in"
--     page and to feed the audit log.
--   * No raw token. We store only `token_hash` (SHA-256 of the
--     opaque session token, doc 06 §5.1). The raw token never
--     leaves the user's cookie jar.

-- =============================================================================
-- sessions
-- =============================================================================

CREATE TABLE IF NOT EXISTS sessions (
    -- Surrogate primary key. UUID v7 keeps inserts B-tree-friendly
    -- (ADR 0003) and gives the audit log a stable handle that is
    -- independent of the cookie token (the token rotates on every
    -- privilege escalation; the row id does not).
    id                  UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- SHA-256 of the opaque session token. The raw token *never*
    -- enters the database — if a Postgres dump leaks, the hashes
    -- alone cannot be replayed against the running system.
    -- `bytea` (32 bytes for SHA-256) rather than hex text to halve
    -- the on-disk footprint and avoid a case-sensitivity foot-gun.
    -- UNIQUE because the cookie token is the lookup key on the fallback
    -- read path.
    token_hash          BYTEA NOT NULL UNIQUE,

    -- Owner. CASCADE on delete: hard-deleting a user removes every
    -- session row in the same transaction, which is what we want for
    -- GDPR erasure (doc 06 §16). Soft-deletion of a user goes through
    -- `users.status = 'deleted'` and leaves sessions intact for audit.
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Creation time. NOT NULL with a default so the row is always
    -- inserted with a wall-clock stamp even if the application omits
    -- the column.
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Sliding deadline. Refreshed on every authenticated request
    -- (subject to a write-amplification throttle — the session
    -- middleware only writes back if `now() - last_seen_at > 60s`).
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Absolute expiry. Hard ceiling; the session is dead at this
    -- instant regardless of activity. Default per install is 90 days
    -- (doc 06 §5.1).
    expires_at          TIMESTAMPTZ NOT NULL,

    -- Idle deadline. Rolling window that advances with `last_seen_at`.
    -- Default per install is 30 days idle (doc 06 §5.1). Stored
    -- separately from `last_seen_at` so the cleanup job can index on
    -- a stable column (the throttle on `last_seen_at` means it isn't
    -- updated on every request, but the idle deadline is what actually
    -- governs expiry).
    idle_expires_at     TIMESTAMPTZ NOT NULL,

    -- Manual-revoke marker. NULL = live (subject to expiry). Non-NULL
    -- = the user (or an admin) revoked the session at this instant;
    -- the row is preserved for audit. The session middleware treats
    -- `revoked_at IS NOT NULL` as "session does not exist" — we keep
    -- the row, not the access.
    revoked_at          TIMESTAMPTZ,

    -- Creation IP. `inet` (not text) so we get native CIDR ops for
    -- the redaction job: after 90 days the recurring privacy task
    -- rewrites this to its `/24` (or `/64` for v6) per doc 06 §5.3,
    -- which is trivial with `set_masklen(ip, 24)`. Nullable because
    -- some flows (e.g. an internal task creating an impersonation
    -- session) have no meaningful client IP.
    ip                  INET,

    -- Raw User-Agent. Truncated to 1024 chars at the write path —
    -- the column is `text` so a bug in the truncator doesn't reject
    -- the row outright, but the auth layer is expected to enforce
    -- the cap. Nullable for the same reasons as `ip`.
    user_agent          TEXT,

    -- Friendly UA-derived label, e.g. "Chrome on macOS" (doc 06
    -- §5.3). Pre-computed at insert time because the UA parser
    -- runs out-of-DB; the column is a denormalised cache so the
    -- "Where you're logged in" UI doesn't re-parse on every render.
    device_label        TEXT,

    -- Free-form extension data. Today it carries the login method
    -- (`password`, `magic_link`, `oauth`, `passkey`) which the audit
    -- log consults, but the column is `jsonb` so future shape changes
    -- (e.g. the impersonator user id, the OAuth provider id) don't
    -- need their own migration.
    meta                JSONB NOT NULL DEFAULT '{}'::jsonb
);

COMMENT ON TABLE  sessions IS
    'Postgres mirror of the Redis session store (PR #291). Source of truth for the /me/sessions UI and the audit trail; Redis is the hot-path source of truth for active sessions. See docs/06-auth-permissions.md §5.';
COMMENT ON COLUMN sessions.token_hash      IS 'SHA-256 of the opaque session token (32 bytes). The raw token is never stored.';
COMMENT ON COLUMN sessions.created_at      IS 'Wall-clock time the session was issued.';
COMMENT ON COLUMN sessions.last_seen_at    IS 'Wall-clock time of the most recent authenticated request. Updated with a throttle to avoid write amplification (see PR #291).';
COMMENT ON COLUMN sessions.expires_at      IS 'Absolute expiry. Session is dead at this instant regardless of activity.';
COMMENT ON COLUMN sessions.idle_expires_at IS 'Sliding idle deadline. Refreshed on use; cleanup job indexes on this column.';
COMMENT ON COLUMN sessions.revoked_at      IS 'Manual revoke marker. Non-NULL = revoked-but-preserved-for-audit. The session middleware treats this as a non-existent session.';
COMMENT ON COLUMN sessions.ip              IS 'Creation IP (inet). Redacted to /24 (v4) or /64 (v6) by the privacy job after 90 days per doc 06 §5.3.';
COMMENT ON COLUMN sessions.user_agent      IS 'Raw User-Agent, truncated to 1024 chars at the write path.';
COMMENT ON COLUMN sessions.device_label    IS 'UA-derived friendly label (e.g. "Chrome on macOS") cached at insert time so the /me/sessions UI does not re-parse on render.';
COMMENT ON COLUMN sessions.meta            IS 'Free-form jsonb: login method (password|magic_link|oauth|passkey), impersonator user id, OAuth provider id, etc.';

-- =============================================================================
-- Indexes
-- =============================================================================

-- "Where you're logged in" lookup (doc 06 §5.3). The /me/sessions
-- page queries `WHERE user_id = $1 AND revoked_at IS NULL ORDER BY
-- last_seen_at DESC` — composite + DESC matches the access pattern
-- exactly, so the planner can satisfy the page from the index alone.
CREATE INDEX IF NOT EXISTS sessions_user_last_seen_idx
    ON sessions (user_id, last_seen_at DESC);

-- Cleanup-job index (doc 06 §5.1). The recurring sweeper runs
-- `DELETE FROM sessions WHERE expires_at < now() AND revoked_at IS
-- NULL` — partial on `revoked_at IS NULL` so the index stays small
-- (revoked rows are kept indefinitely for audit and would otherwise
-- bloat the index forever).
CREATE INDEX IF NOT EXISTS sessions_expires_active_idx
    ON sessions (expires_at)
    WHERE revoked_at IS NULL;

-- Active-session counts per user. The auth layer enforces a per-user
-- ceiling on concurrent sessions (configurable, default 25) by
-- counting rows where `user_id = $1 AND revoked_at IS NULL AND
-- expires_at > now()`. Partial on `revoked_at IS NULL` for the same
-- reason as the cleanup index.
CREATE INDEX IF NOT EXISTS sessions_user_active_idx
    ON sessions (user_id)
    WHERE revoked_at IS NULL;
