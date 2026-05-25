-- 000035_media_collections.up.sql
--
-- Media collections — hierarchical folders that group media assets.
-- The hierarchy is stored two ways: a parent_id self-reference (so a
-- "move" is a single column update) and a materialised ltree path
-- (so descendant/ancestor lookups are an index scan, not a recursive
-- CTE).
--
-- Why ltree instead of an adjacency-list-only design? The admin
-- folder tree's hot path is "list every descendant of /Marketing"
-- — a recursive CTE would work but the optimiser cannot prune it,
-- and at thousands of folders it shows up in the slow-query log.
-- ltree's GiST index turns the same lookup into a sub-millisecond
-- range scan. The parent_id column rides alongside so the move
-- operation has a single column to mutate; the path column is
-- updated by the application after the parent_id change, inside the
-- same transaction.
--
-- The slug column is citext so two folders named "Marketing" and
-- "marketing" don't both exist under the same parent — Postgres
-- handles the case-insensitive uniqueness check.

CREATE EXTENSION IF NOT EXISTS ltree;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE media_collections (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        CITEXT NOT NULL
                CHECK (length(slug) > 0 AND length(slug) <= 64
                       AND slug ~ '^[a-z0-9][a-z0-9_-]*$'),
    name        TEXT NOT NULL
                CHECK (length(name) > 0 AND length(name) <= 256),
    path        LTREE NOT NULL,
    parent_id   UUID REFERENCES media_collections(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Two folders under the same parent cannot share a slug.
    -- The NULL parent_id case is the root, which we model with a
    -- separate partial index below (NULLs are not equal in btree
    -- unique indexes).
    UNIQUE (parent_id, slug)
);

-- Partial unique index for root-level slugs (parent_id IS NULL).
-- Without this, multiple roots could share "marketing".
CREATE UNIQUE INDEX media_collections_root_slug_idx
    ON media_collections (slug)
    WHERE parent_id IS NULL;

-- GiST on path is the workhorse: ancestor (@>), descendant (<@), and
-- lquery match operators all use this index. Plain btree won't do
-- because ltree's specialised operators only kick in with a GiST or
-- a SP-GiST opclass.
CREATE INDEX media_collections_path_gist_idx
    ON media_collections USING GIST (path);

-- A btree on the path supports exact-match lookups and ordering by
-- path (the admin tree renders depth-first; the ORDER BY uses this).
CREATE INDEX media_collections_path_btree_idx
    ON media_collections (path);

-- Parent_id index covers the "list direct children of X" query the
-- tree sidebar issues on every expand. Without it Postgres
-- sequence-scans the whole table for each click.
CREATE INDEX media_collections_parent_idx
    ON media_collections (parent_id);

-- Hook the media row at the collection. Nullable: media without a
-- folder live in the implicit "root" view alongside the empty-string
-- path. ON DELETE SET NULL because deleting a folder should NOT
-- delete the media inside it — the media surface back up to the
-- root view, and the operator can re-file them.
ALTER TABLE media
    ADD COLUMN collection_id UUID REFERENCES media_collections(id) ON DELETE SET NULL;

-- Index the FK so the "list media in collection X" query the grid
-- issues on every folder click is a constant-cost lookup.
CREATE INDEX media_collection_id_idx ON media (collection_id);
