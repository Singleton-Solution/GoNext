-- 000024_personal_access_tokens.up.sql
--
-- `personal_access_tokens` — long-lived bearer tokens for programmatic
-- API access (CI, CLI, external scripts). Unlike browser sessions
-- (000009_sessions) these tokens:
--
--   * Have no cookie path; they ride in `Authorization: Bearer gnp_*`.
--   * Carry an explicit, narrower scope set than the issuing user's
--     own capabilities. The auth middleware intersects scopes with the
--     user's caps at request time, so revoking a user's role also
--     defangs their tokens automatically.
--   * Are never re-displayed after issuance. The plaintext appears in
--     the create response exactly once; the database stores only the
--     argon2id hash, identical posture to passwords (000002 §user_passwords).
--
-- Sessions (#291) are short-lived and rotate every privilege escalation;
-- PATs are explicitly long-lived and only die when revoked or expired.
-- The two stores intentionally don't share a row — a token revoked here
-- must NOT log out the user's browser, and a logged-out browser must
-- NOT defang the CI token.
--
-- This migration depends on:
--   * 000001_init  — `pgcrypto`, `gen_uuid_v7()`.
--   * 000002_users — `users(id)` for the FK.

-- =============================================================================
-- personal_access_tokens
-- =============================================================================

CREATE TABLE IF NOT EXISTS personal_access_tokens (
    -- Surrogate primary key. UUID v7 keeps inserts B-tree-friendly
    -- (ADR 0003) and gives revocation a stable handle independent of
    -- the bearer string.
    id                  UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Owner. CASCADE on delete: hard-deleting a user removes every
    -- token row in the same transaction. Soft-delete of the user
    -- (status='deleted') leaves the row intact for audit; the
    -- middleware refuses the token because the user lookup fails.
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Human label the operator picks at creation ("github-actions",
    -- "laptop CLI", etc.). Required so the revoke page has something
    -- to render — the prefix alone is unfriendly.
    name                TEXT NOT NULL CHECK (length(btrim(name)) > 0),

    -- First 8 chars of the bearer (after the `gnp_` namespace). Lets
    -- the list view show "gnp_AbCdEfGh…" without storing the full
    -- token. The full string lives only on the operator's clipboard.
    -- CHAR(8) (not VARCHAR) because the length is fixed at issuance.
    prefix              CHAR(8) NOT NULL,

    -- argon2id PHC string of the FULL plaintext token (prefix + suffix).
    -- Stored as bytea via the encoded PHC bytes for parity with the
    -- session token_hash column shape, and UNIQUE so a (vanishingly
    -- unlikely) collision is caught at insert rather than at lookup.
    -- We compare in constant time at lookup; the UNIQUE index also
    -- means an attacker probing for hash prefixes gets nothing useful
    -- back (the planner answers "exists" without revealing identity).
    hash                BYTEA NOT NULL UNIQUE,

    -- Scopes the token is allowed to exercise. Stored as a TEXT[] of
    -- capability slugs (matching packages/go/policy/capabilities.go).
    -- The middleware intersects this list with the user's effective
    -- caps at every request — narrower of the two wins. Empty array
    -- means a deny-all token (useless but legal; we don't enforce
    -- non-empty because the create handler does).
    scopes              TEXT[] NOT NULL DEFAULT '{}'::text[],

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Refreshed on every successful authentication. The middleware
    -- writes back with the same 60s throttle the session store uses
    -- (see 000009) so a token in heavy CI traffic isn't a hot row.
    -- NULLABLE because a freshly issued token has never been used.
    last_used_at        TIMESTAMPTZ,

    -- Optional absolute expiry. NULL = never expires (until revoked).
    -- The UI offers preset durations (30d, 90d, 1y) and "no expiry";
    -- the column is plain TIMESTAMPTZ so future custom durations
    -- don't need their own migration.
    expires_at          TIMESTAMPTZ,

    -- Manual-revoke marker. Mirrors the sessions table semantics: NULL
    -- = live (subject to expiry), non-NULL = revoked-but-preserved-for-
    -- audit. The middleware treats `revoked_at IS NOT NULL` as 401.
    revoked_at          TIMESTAMPTZ
);

COMMENT ON TABLE  personal_access_tokens IS
    'Long-lived bearer tokens for programmatic API access. Issued once, hashed at rest, revocable. Distinct from sessions (#291): no cookie, no rotation, explicit narrow scopes intersected with the user''s caps at request time.';
COMMENT ON COLUMN personal_access_tokens.user_id      IS 'Owning user. CASCADE on hard-delete; preserved on soft-delete for audit.';
COMMENT ON COLUMN personal_access_tokens.name         IS 'Operator-chosen label rendered in the "my tokens" list. Required; trimmed non-empty.';
COMMENT ON COLUMN personal_access_tokens.prefix       IS 'First 8 chars of the bearer (post the gnp_ namespace). Used for "gnp_AbCdEfGh…" display only.';
COMMENT ON COLUMN personal_access_tokens.hash         IS 'argon2id of the full plaintext token. Constant-time compared at lookup. UNIQUE.';
COMMENT ON COLUMN personal_access_tokens.scopes       IS 'Capability slugs this token may exercise. Intersected with the user''s effective caps at request time.';
COMMENT ON COLUMN personal_access_tokens.last_used_at IS 'Wall-clock of the most recent successful auth. Updated with the same 60s throttle the session mirror uses.';
COMMENT ON COLUMN personal_access_tokens.expires_at   IS 'Optional absolute expiry. NULL = never expires (until revoked).';
COMMENT ON COLUMN personal_access_tokens.revoked_at   IS 'Manual revoke marker. Non-NULL = revoked-but-preserved-for-audit; treated as 401 at the auth path.';

-- =============================================================================
-- Indexes
-- =============================================================================

-- "My active tokens" lookup. The /me/tokens page filters on
-- `user_id = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR
-- expires_at > now())` and orders by created_at DESC. The composite
-- index covers the equality + IS NULL predicates; the planner can
-- satisfy the page from the index alone for the common (no-expiry)
-- case.
CREATE INDEX IF NOT EXISTS personal_access_tokens_user_active_idx
    ON personal_access_tokens (user_id, revoked_at, expires_at);

-- Expiry sweep. The recurring cleanup job runs
-- `UPDATE personal_access_tokens SET revoked_at = expires_at WHERE
-- expires_at < now() AND revoked_at IS NULL`. Partial on
-- `revoked_at IS NULL` so the index stays small (revoked rows are
-- never revisited by the sweeper).
CREATE INDEX IF NOT EXISTS personal_access_tokens_expires_active_idx
    ON personal_access_tokens (expires_at)
    WHERE revoked_at IS NULL AND expires_at IS NOT NULL;
