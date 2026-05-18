-- 000015_migration_map.up.sql
--
-- Durable mapping table for the data-migration importer (issue #147,
-- companion to the WXR importer in #144).
--
-- Problem this solves: importing a third-party export (e.g. a
-- WordPress WXR file, a Ghost JSON dump) generates a new GoNext
-- entity for every source record — a new user, a new post, a new
-- term. To make the import idempotent (re-running the same file is
-- a no-op) and to make intra-export references resolve (post X
-- belongs to tag Y, where Y is a WP term_id that we've already
-- inserted as a GoNext term UUID), we need a persistent table that
-- answers the question
--
--     "given source 'wp' + entity_type 'term' + source_id '7',
--      which GoNext UUID did I assign last time?"
--
-- The table is intentionally agnostic about the source. The
-- importer for any external system writes the same rows; the only
-- difference is the value of the `source` column. This means a
-- single GoNext database can host content imported from many
-- different upstream systems without the schema knowing about any
-- of them.
--
-- The (source, entity_type, source_id) compound primary key is
-- what makes re-imports safe: the importer wraps every insert in
-- ON CONFLICT DO UPDATE, so a second run sees the existing row
-- and skips creating a duplicate target. The `meta` JSONB column
-- gives importers a place to stash provenance ("the WP user had
-- email X at import time") for later diagnostics without forcing
-- a schema change every time we discover a new datum we wish we'd
-- preserved.
--
-- Depends on:
--   * 000001_init — JSONB and TIMESTAMPTZ come from the standard
--                   extension set installed there. UUID v7 is NOT
--                   used for primary keys here (the natural key is
--                   the source tuple); UUID columns simply use the
--                   builtin uuid type.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE migration_map (
    -- Origin system identifier. Free-form text rather than an enum
    -- so adding a new importer (e.g. Ghost, Substack) is a code
    -- change only — no migration required. Well-known values are
    -- enumerated as constants in packages/go/migrate/migmap/types.go;
    -- importers SHOULD use those rather than spelling the literal
    -- inline.
    source          TEXT NOT NULL
                    CHECK (length(source) > 0 AND length(source) <= 64),

    -- The kind of GoNext entity the target_id refers to. Constrained
    -- by CHECK so a typo in the importer ("usre") fails loudly at
    -- the database tier rather than producing rows that nothing
    -- will ever query. Adding a new entity type requires a migration
    -- — this is intentional, because new entity types usually mean
    -- new join targets we want the application layer to be aware of.
    entity_type     TEXT NOT NULL
                    CHECK (entity_type IN (
                        'user',
                        'post',
                        'term',
                        'comment',
                        'attachment'
                    )),

    -- The source system's native identifier. Stringified because
    -- WP uses bigint user_ids, Ghost uses opaque slugs, and Medium
    -- uses base36 — picking one numeric type would either pad-pack
    -- strings or truncate IDs. Length-capped at 255 to keep the
    -- composite index leaf size predictable.
    source_id       TEXT NOT NULL
                    CHECK (length(source_id) > 0 AND length(source_id) <= 255),

    -- The GoNext UUID this source record was mapped to. Not a
    -- foreign key — `target_id` may point into any of half a dozen
    -- tables (users, posts, terms, ...) depending on entity_type,
    -- and a polymorphic FK is more pain than the integrity benefit
    -- is worth. The importer is responsible for ensuring the target
    -- exists; the GetByTarget reverse lookup tolerates dangling
    -- pointers (a deleted user leaves a stale row, which the next
    -- prune sweep can clean up).
    target_id       UUID NOT NULL,

    -- When this mapping was first written. Preserved across
    -- subsequent ON CONFLICT updates so the original import date is
    -- always available for forensics; updates touch only `meta`.
    imported_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Per-mapping provenance bag. Importers stash whatever they
    -- want here — original login name, source URL, content hash —
    -- so a later diagnostic ("why is this post empty?") has the
    -- original record to compare against. JSONB rather than TEXT
    -- so operators can query meta->>'wp_login' from psql.
    --
    -- On conflict the meta is *merged* (jsonb concatenation
    -- operator), not replaced — this lets a second-pass importer
    -- add a `revisions_imported_at` key without destroying the
    -- first pass's `original_login` key.
    meta            JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- The natural primary key. The importer's hot path is
    -- "given (source, entity_type, source_id), find target_id" —
    -- a single index probe.
    PRIMARY KEY (source, entity_type, source_id)
);

-- =============================================================================
-- Indexes
-- =============================================================================

-- Reverse lookup: "which source records map to this GoNext entity?"
-- Used when an admin opens a record and wants to see "imported from
-- WP post 42 on 2026-01-04" provenance. Also used by the test suite
-- to assert that a re-import didn't double-create.
--
-- Not unique: in principle two different sources could legitimately
-- alias to the same GoNext entity (a manual merge). The application
-- doesn't currently do that, but the schema shouldn't forbid it.
CREATE INDEX migration_map_target_id_idx
    ON migration_map (target_id);
