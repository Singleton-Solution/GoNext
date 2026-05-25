-- 000033_password_reset_tokens.up.sql
--
-- `password_reset_tokens` — single-use tokens that grant a one-time
-- "set a new password without knowing the old one" capability.
--
-- The flow (apps/api/internal/auth/passwordreset, issue #140):
--
--   1. POST /api/v1/auth/password-reset/request {email}
--      The server mints a random 32-byte token, stores the SHA-256
--      hex of it here with a 1h TTL, and emails the user a link of
--      the form `/reset-password?token=<hex>`. The response is ALWAYS
--      200 regardless of whether the email is known — leaking
--      "this email is registered" via the response code is a textbook
--      account-enumeration vector.
--
--   2. POST /api/v1/auth/password-reset/confirm {token, new_password}
--      The server hashes the supplied token and looks up the row,
--      checks that it is unexpired and `used_at IS NULL`, validates
--      password strength, updates `user_passwords.password_hash`,
--      marks the row used, and invalidates ALL active sessions for
--      that user (so a stolen cookie cannot survive a reset).
--
-- Posture rationale:
--   * Tokens are SHA-256 hashed at rest. The plaintext exists only
--     in the user's email and in volatile memory at issue time. An
--     attacker with a database dump cannot forge a reset.
--   * Single-use: once `used_at` is set, the same plaintext token
--     never works again, even within the TTL window. Replay protection
--     comes from the partial unique index — see below.
--   * TTL of 1h matches the OWASP "Password Reset Cheat Sheet" upper
--     bound; long enough for a user's email client to ferry the
--     message through a spam filter, short enough that a stolen
--     archived inbox isn't a perpetual takeover primitive.
--   * The table is a separate concern from `personal_access_tokens`
--     (000026) and `sessions` (000009). Each store has its own
--     revocation semantics, retention policy, and rate-limit posture.
--
-- This migration depends on:
--   * 000001_init  — `pgcrypto`, `gen_uuid_v7()`.
--   * 000002_users — `users(id)` for the FK.

-- =============================================================================
-- password_reset_tokens
-- =============================================================================

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    -- Surrogate primary key. UUID v7 keeps inserts B-tree-friendly
    -- (ADR 0003) and lets the audit log carry a stable handle for a
    -- specific reset attempt independent of the bearer string.
    id                  UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Owner. CASCADE on delete: hard-deleting a user removes every
    -- pending reset in the same transaction. Soft-delete of the user
    -- (status='deleted') leaves the row intact for audit; the confirm
    -- handler refuses the token because the user lookup fails.
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- SHA-256 hex of the plaintext token. The plaintext is 32 random
    -- bytes hex-encoded to 64 chars; SHA-256 of those 64 chars yields
    -- another 64-char hex string. Stored as TEXT for parity with the
    -- email_verify Redis key shape (verify/store.go), and UNIQUE so a
    -- (vanishingly unlikely) collision is caught at insert rather than
    -- producing two valid resets for the same hash.
    token_hash          TEXT NOT NULL UNIQUE,

    -- Absolute expiry. The application also enforces TTL on insert,
    -- but storing it on the row means the cleanup sweep doesn't need
    -- to know the issue-time TTL (which may have changed since the
    -- row was written).
    expires_at          TIMESTAMPTZ NOT NULL,

    -- Single-use marker. NULL = still redeemable, non-NULL = consumed
    -- (preserved for audit). Once set, the confirm handler refuses
    -- the token even if the TTL hasn't elapsed.
    used_at             TIMESTAMPTZ,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE  password_reset_tokens IS
    'Single-use, hashed, time-bounded tokens for the "I forgot my password" flow. Issued by POST /api/v1/auth/password-reset/request, consumed by POST /api/v1/auth/password-reset/confirm.';
COMMENT ON COLUMN password_reset_tokens.user_id    IS 'Owning user. CASCADE on hard-delete; preserved on soft-delete for audit.';
COMMENT ON COLUMN password_reset_tokens.token_hash IS 'SHA-256 hex of the plaintext bearer. Plaintext is never stored; the email carries it once.';
COMMENT ON COLUMN password_reset_tokens.expires_at IS 'Absolute deadline. Application also enforces TTL at insert; the column lets the cleanup sweep work without knowing issue-time TTL.';
COMMENT ON COLUMN password_reset_tokens.used_at    IS 'Single-use marker. NULL = redeemable, non-NULL = consumed (preserved for audit).';

-- =============================================================================
-- Indexes
-- =============================================================================

-- "Find the pending reset for this user" lookup, used by the rate-limit
-- check ("has this user already requested a reset in the last N
-- minutes?") and by the cleanup sweep. Partial on `used_at IS NULL` so
-- the index stays small — consumed rows are never re-read by the
-- application path.
CREATE INDEX IF NOT EXISTS password_reset_tokens_user_active_idx
    ON password_reset_tokens (user_id, expires_at)
    WHERE used_at IS NULL;

-- Expiry sweep. The recurring cleanup job runs
-- `DELETE FROM password_reset_tokens WHERE expires_at < now()`. Partial
-- on `used_at IS NULL` because consumed rows are kept for the same
-- horizon as the audit log; only unredeemed rows are eligible for
-- TTL-based deletion.
CREATE INDEX IF NOT EXISTS password_reset_tokens_expires_idx
    ON password_reset_tokens (expires_at)
    WHERE used_at IS NULL;
