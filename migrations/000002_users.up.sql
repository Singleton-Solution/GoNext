-- 000002_users.up.sql
--
-- Identity tables: `users` and `user_passwords`.
--
-- This migration introduces the minimum surface the CMS needs to
-- attribute content to humans (see docs/01-core-cms.md §10.3) plus the
-- separate password material table required by the auth design
-- (docs/06-auth-permissions.md §2.2).
--
-- Two tables, deliberately split:
--
--   * users           — the "identity" row. Cheap to SELECT, safe to log,
--                       referenced by every FK that wants an author.
--   * user_passwords  — credential material. Kept out of `users` so a
--                       routine `SELECT * FROM users` cannot leak hashes
--                       to API responses or log lines (doc 06 §2.2 note).
--
-- All FKs from later migrations land on `users(id)` (UUID v7), never on
-- a serial — see ADR 0003 and the FK columns in doc 01 §10.5.

-- =============================================================================
-- users
-- =============================================================================

CREATE TABLE IF NOT EXISTS users (
    -- Time-sortable UUID v7 (see 000001 for gen_uuid_v7 + ADR 0003).
    id                  UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- citext: case-insensitive uniqueness without a functional index.
    -- `Alice@Example.com` and `alice@example.com` collide naturally.
    email               CITEXT NOT NULL UNIQUE,

    -- NULL = unverified. Set on successful email confirmation.
    email_verified_at   TIMESTAMPTZ,

    -- Display name / login handle. Separate from email per doc 06 §2.3
    -- (login form accepts either). citext for the same case-insensitive
    -- semantics as email.
    handle              CITEXT NOT NULL UNIQUE,

    -- Human-facing label. Distinct from `handle` so a user can pick a
    -- friendly name without changing every URL that references them.
    display_name        TEXT,

    bio                 TEXT,
    avatar_url          TEXT,

    -- BCP-47 language tag. Default matches the platform's default UI
    -- locale; the value is plain text rather than an enum so plugins can
    -- introduce new locales without a migration.
    locale              TEXT NOT NULL DEFAULT 'en',

    -- IANA timezone name (e.g. 'America/Los_Angeles'). Stored as text
    -- for the same reason as locale.
    timezone            TEXT NOT NULL DEFAULT 'UTC',

    -- Lifecycle state. CHECK enforces the closed set without committing
    -- to a Postgres ENUM (cheaper to extend later — doc 06 §2.2 uses
    -- the same text-with-check pattern).
    status              TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'suspended', 'deleted')),

    -- User-level extension data. Plugins write here through the meta API
    -- (see doc 01 §10.1). jsonb (not json) so we can GIN-index it below.
    meta                JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Optimistic concurrency token. Incremented by the touch trigger on
    -- every UPDATE so the application layer can detect lost updates
    -- with `WHERE version = :expected`.
    version             INTEGER NOT NULL DEFAULT 1
);

COMMENT ON TABLE  users IS 'Core identity row. One row per human (or service principal). Password material lives in user_passwords.';
COMMENT ON COLUMN users.email   IS 'Case-insensitive (citext). Unique. NOT a stable identifier — users can change it.';
COMMENT ON COLUMN users.handle  IS 'Case-insensitive (citext). Unique. Display name / login handle, separate from email per doc 06 §2.3.';
COMMENT ON COLUMN users.status  IS 'Lifecycle: active | suspended | deleted. Soft-delete uses ''deleted''; rows are retained for FK integrity.';
COMMENT ON COLUMN users.version IS 'Optimistic concurrency token. Bumped by the touch trigger on every UPDATE.';

-- Partial unique index on lower(email) for the active+suspended subset.
-- Belt-and-braces on top of the citext UNIQUE: when a row is soft-deleted
-- (status = 'deleted'), this index releases the email so a new account
-- can claim it without first hard-deleting the old row. The citext
-- UNIQUE still prevents two *live* accounts from sharing an email.
--
-- Note: deletion is expected to either (a) anonymise the email column
-- (GDPR job, doc 06 §16) or (b) leave it intact for audit. The partial
-- index gives the operator the choice; neither path conflicts.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_active_idx
    ON users (lower(email))
    WHERE status <> 'deleted';

-- GIN with jsonb_path_ops: smaller and faster than the default jsonb_ops
-- for the queries we actually run against meta (containment via @>).
-- See https://www.postgresql.org/docs/16/datatype-json.html#JSON-INDEXING.
CREATE INDEX IF NOT EXISTS users_meta_gin
    ON users USING gin (meta jsonb_path_ops);

-- =============================================================================
-- updated_at + version touch trigger
-- =============================================================================
--
-- Generic trigger function reusable by later tables that follow the
-- same (updated_at, version) optimistic-concurrency pattern. Lives in
-- this migration because `users` is the first table to need it.

CREATE OR REPLACE FUNCTION touch_updated_at_and_version()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at := now();
    -- COALESCE guards against an explicit NULL set by the application.
    NEW.version    := COALESCE(OLD.version, 0) + 1;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION touch_updated_at_and_version() IS
    'BEFORE UPDATE trigger body. Stamps updated_at = now() and bumps version. Reusable across tables that follow the optimistic-concurrency pattern.';

CREATE TRIGGER users_touch_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION touch_updated_at_and_version();

-- =============================================================================
-- user_passwords
-- =============================================================================
--
-- Per doc 06 §2.2: password material lives in its own table so that a
-- `SELECT * FROM users` cannot accidentally surface hashes to logs or
-- API responses. One row per user; rotating a password updates the row
-- (this is the shape the rate-limit FailureStore in issue #296 expects).

CREATE TABLE IF NOT EXISTS user_passwords (
    -- PK = FK. One credential row per user; ON DELETE CASCADE so hard-
    -- deleting a user removes the password row in the same transaction.
    user_id             UUID PRIMARY KEY
                        REFERENCES users(id) ON DELETE CASCADE,

    -- Argon2id PHC string, e.g.
    --   $argon2id$v=19$m=65536,t=3,p=2$<salt-b64>$<hash-b64>
    -- Stored as text so we can swap parameters in a single column
    -- (the embedded params are how verifiers know which cost to use).
    password_hash       TEXT NOT NULL,

    -- Bumped when the cluster-wide argon2id parameters change. A
    -- background job re-hashes rows where params_version < current at
    -- next login (the user submits the plaintext, we rehash, write back).
    params_version      INTEGER NOT NULL DEFAULT 1,

    last_changed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Counters for the rate-limit FailureStore (issue #296 / doc 06
    -- §12.2). Kept on the password row rather than `users` so that a
    -- routine identity SELECT doesn't touch counters that change on
    -- every failed login.
    failed_login_count  INTEGER NOT NULL DEFAULT 0,
    locked_until        TIMESTAMPTZ
);

COMMENT ON TABLE  user_passwords IS 'Password material, kept separate from users so identity reads never surface hashes (doc 06 §2.2).';
COMMENT ON COLUMN user_passwords.password_hash      IS 'Argon2id PHC string. The embedded params drive verification cost.';
COMMENT ON COLUMN user_passwords.params_version     IS 'Cluster-wide argon2id param generation. Used to schedule background rehashing on next login.';
COMMENT ON COLUMN user_passwords.failed_login_count IS 'Rate-limit FailureStore counter (issue #296 / doc 06 §12.2). Reset on successful auth.';
COMMENT ON COLUMN user_passwords.locked_until       IS 'Account-lockout deadline (doc 06 §12.2). NULL = not locked.';
