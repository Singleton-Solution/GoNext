-- 000001_init.up.sql
--
-- Foundation migration: enable the extensions and define the SQL
-- primitives that every subsequent migration depends on.
--
-- Scope is deliberately narrow:
--   * Postgres extensions used across the schema (pgcrypto, citext, ltree).
--   * gen_uuid_v7() — the canonical primary-key generator (ADR 0003).
--   * Core enums that appear in multiple tables (post_status, revision_kind).
--
-- No tables are created here. Concrete tables land in subsequent
-- migrations (#39 onward) so that each table change has its own
-- reviewable, revertible pair of files.

-- =============================================================================
-- Extensions
-- =============================================================================

-- pgcrypto: gen_random_uuid() as a fallback random source and (more
-- importantly) the family of crypto helpers we use elsewhere
-- (digest(), hmac()). The IF NOT EXISTS guard means re-running this
-- migration on a database that has the extension already is a no-op.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- citext: case-insensitive text. Used for emails and usernames so we
-- don't have to remember to LOWER() on every lookup.
CREATE EXTENSION IF NOT EXISTS citext;

-- ltree: materialised-path hierarchy. Used by taxonomies (category
-- trees) and the threaded-comment design (doc 01 §6).
CREATE EXTENSION IF NOT EXISTS ltree;

-- =============================================================================
-- gen_uuid_v7() — RFC 9562 UUID v7 generator
-- =============================================================================
--
-- We pin every primary key to UUID v7 (ADR 0003). v7 stores a 48-bit
-- Unix-millisecond timestamp in the high bits, then a 4-bit version,
-- then 74 bits of random data. The timestamp prefix makes v7 IDs
-- B-tree-friendly: inserts cluster at the tail of the index, instead
-- of scattering across the whole page tree the way v4 does.
--
-- Layout (RFC 9562 §5.7):
--
--   0                   1                   2                   3
--   0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
--   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
--   |                           unix_ts_ms                          |
--   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
--   |          unix_ts_ms           |  ver  |       rand_a          |
--   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
--   |var|                        rand_b                             |
--   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
--   |                            rand_b                             |
--   +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
--
-- Implementation: take a fresh 16-byte random UUID, then overwrite
-- the first 6 bytes with the millisecond timestamp and stamp the
-- version (0111 = 7) and variant (10) nibbles. This is the pattern
-- used by every reference UUID v7 generator (e.g. fboulnois/pg_uuidv7).
CREATE OR REPLACE FUNCTION gen_uuid_v7()
RETURNS UUID
LANGUAGE plpgsql
VOLATILE
PARALLEL SAFE
AS $$
DECLARE
    unix_ts_ms BIGINT;
    uuid_bytes BYTEA;
BEGIN
    -- Millisecond Unix timestamp. extract(epoch) returns a double, *1000
    -- gives ms, ::bigint truncates. Postgres' clock_timestamp() is the
    -- right source here — it always returns wall time, never the
    -- transaction-start time.
    unix_ts_ms := (EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT;

    -- Fresh random 16 bytes from gen_random_uuid() (pgcrypto).
    uuid_bytes := uuid_send(gen_random_uuid());

    -- Overwrite bytes 0..5 with the 48-bit big-endian timestamp.
    -- set_byte is 0-indexed.
    uuid_bytes := set_byte(uuid_bytes, 0, ((unix_ts_ms >> 40) & 255)::INT);
    uuid_bytes := set_byte(uuid_bytes, 1, ((unix_ts_ms >> 32) & 255)::INT);
    uuid_bytes := set_byte(uuid_bytes, 2, ((unix_ts_ms >> 24) & 255)::INT);
    uuid_bytes := set_byte(uuid_bytes, 3, ((unix_ts_ms >> 16) & 255)::INT);
    uuid_bytes := set_byte(uuid_bytes, 4, ((unix_ts_ms >> 8) & 255)::INT);
    uuid_bytes := set_byte(uuid_bytes, 5, (unix_ts_ms & 255)::INT);

    -- Version nibble (byte 6, high 4 bits) → 0111 = 7.
    uuid_bytes := set_byte(uuid_bytes, 6, ((get_byte(uuid_bytes, 6) & 15) | 112));

    -- Variant nibble (byte 8, high 2 bits) → 10.
    uuid_bytes := set_byte(uuid_bytes, 8, ((get_byte(uuid_bytes, 8) & 63) | 128));

    RETURN encode(uuid_bytes, 'hex')::UUID;
END;
$$;

COMMENT ON FUNCTION gen_uuid_v7() IS
    'Returns a UUID v7 (RFC 9562): 48-bit unix-ms timestamp + 4-bit version + 74 bits random. Use as DEFAULT for every primary key column.';

-- =============================================================================
-- Core enums
-- =============================================================================
--
-- These are referenced by tables that arrive in later migrations. We
-- create them up front so the rest of the schema can DEFAULT to them
-- without each migration redoing the definition.

-- post_status drives the publishing workflow. The 'revision' value
-- exists so that historical revisions can share the posts table
-- discriminator without inventing a parallel status enum. See
-- docs/01-core-cms.md §4 (publishing) and §5 (revisions).
CREATE TYPE post_status AS ENUM (
    'draft',
    'pending',
    'scheduled',
    'published',
    'private',
    'trash',
    'revision'
);

-- revision_kind tags rows in post_revisions: autosaves are noisy and
-- get garbage-collected aggressively; manual saves and publish points
-- are kept indefinitely.
CREATE TYPE revision_kind AS ENUM (
    'autosave',
    'manual',
    'publish'
);
