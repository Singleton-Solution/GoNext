-- 000034_magic_link_tokens.up.sql
--
-- `magic_link_tokens` — single-use, short-lived passwordless login
-- bearers. The flow (apps/api/internal/auth/magiclink, issue #203):
--
--   1. POST /api/v1/auth/magic-link/request {email}
--      The server mints a random 32-byte token, stores the SHA-256
--      hex of it here with a 15-minute TTL, and emails the user a
--      link of the form `/auth/magic-link?token=<hex>`. The response
--      is ALWAYS 200 regardless of whether the email is known —
--      same enumeration-safe posture as password reset (000033).
--
--   2. GET /api/v1/auth/magic-link?token=<hex>
--      The server hashes the supplied token, checks that it is
--      unexpired and `used_at IS NULL`, mints a fresh session using
--      packages/go/session, marks the row used, and redirects to /.
--      The session inherits the deployment's standard SessionTTL /
--      SessionIdleTTL — magic-link auth is one path to a normal
--      session, not a separate lower-trust mode.
--
-- Why a separate table from `password_reset_tokens` (000033)?
--   * Different TTL (15 min vs 1h). Inlining the two into a polymorphic
--     "auth_tokens" table would either pick a single TTL (wrong for
--     one of the flows) or carry a `purpose` column with redundant
--     index pressure.
--   * Different consumption semantics: password-reset confirms with a
--     POST body (token + new password), magic-link consumes via GET
--     (link click). Keeping the stores separate lets each handler
--     enforce its own request shape without conditional branches.
--   * Different rate-limit policies. Both happen to share the
--     5 req / 15 min envelope today, but locking that into the schema
--     would constrain future divergence.
--   * Audit/forensic separation: a flurry of magic-link requests is a
--     different signal from a flurry of password-reset requests. Joining
--     by `user_id` across the two tables keeps the per-flow analysis
--     clean without forcing every reader to apply a WHERE purpose=.
--
-- Posture rationale: identical to 000033 — tokens are SHA-256 hashed at
-- rest, single-use, and time-bounded. See that migration's header for
-- the full rationale; this table mirrors the schema with a tighter TTL
-- enforced by the application layer (the column type is the same).
--
-- This migration depends on:
--   * 000001_init  — `pgcrypto`, `gen_uuid_v7()`.
--   * 000002_users — `users(id)` for the FK.

-- =============================================================================
-- magic_link_tokens
-- =============================================================================

CREATE TABLE IF NOT EXISTS magic_link_tokens (
    -- Surrogate primary key. UUID v7 keeps inserts B-tree-friendly
    -- (ADR 0003) and gives the audit log a stable handle independent
    -- of the bearer string.
    id                  UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Owner. CASCADE on delete: hard-deleting a user removes every
    -- pending magic link in the same transaction.
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- SHA-256 hex of the plaintext token. See 000033 for the rationale
    -- on the hex-text shape.
    token_hash          TEXT NOT NULL UNIQUE,

    -- Absolute expiry. Application enforces a 15-minute TTL at insert;
    -- the column lets the cleanup sweep work without re-deriving it.
    expires_at          TIMESTAMPTZ NOT NULL,

    -- Single-use marker. NULL = still redeemable, non-NULL = consumed
    -- (preserved for audit).
    used_at             TIMESTAMPTZ,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE  magic_link_tokens IS
    'Single-use, hashed, short-lived bearers for the passwordless magic-link login flow. Issued by POST /api/v1/auth/magic-link/request, consumed by GET /api/v1/auth/magic-link.';
COMMENT ON COLUMN magic_link_tokens.user_id    IS 'Owning user. CASCADE on hard-delete; preserved on soft-delete for audit.';
COMMENT ON COLUMN magic_link_tokens.token_hash IS 'SHA-256 hex of the plaintext bearer. Plaintext is never stored; the email carries it once.';
COMMENT ON COLUMN magic_link_tokens.expires_at IS '15-minute TTL enforced at issuance. Column lets the cleanup sweep operate without re-deriving the deadline.';
COMMENT ON COLUMN magic_link_tokens.used_at    IS 'Single-use marker. NULL = redeemable, non-NULL = consumed (preserved for audit).';

-- =============================================================================
-- Indexes
-- =============================================================================

-- "Find the pending magic link for this user" lookup. Same shape as
-- password_reset_tokens — partial on `used_at IS NULL` so consumed
-- rows don't bloat the index.
CREATE INDEX IF NOT EXISTS magic_link_tokens_user_active_idx
    ON magic_link_tokens (user_id, expires_at)
    WHERE used_at IS NULL;

-- Expiry sweep, partial on `used_at IS NULL`. See 000033 for the
-- rationale on partial-index choice for the cleanup path.
CREATE INDEX IF NOT EXISTS magic_link_tokens_expires_idx
    ON magic_link_tokens (expires_at)
    WHERE used_at IS NULL;
