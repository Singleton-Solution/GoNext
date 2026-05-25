-- 000032_reusable_blocks.up.sql
--
-- Storage for "reusable" (synced) blocks — named block-tree snippets
-- that any post can reference. Inserting a reusable block in the
-- editor materialises as `{type: "core/block", ref: "<uuid>"}`; the
-- renderer resolves the ref to this row's `content` on read, and an
-- edit on the inlined surface writes back here, propagating to every
-- instance of the same UUID.
--
-- This is GoNext's analogue of WordPress's wp_block post-type. We
-- promote it to a first-class table instead of a post-type because
-- the access pattern is "look up by UUID, return JSONB" — a posts
-- query plus an attribute decode would burn cycles for the most
-- common path. See issue #193 for the full rationale.
--
-- Schema:
--
--   id         — uuid primary key, embedded in core/block ref.
--   name       — human-readable label, shown in the inserter. Not
--                unique: two authors can land on the same name.
--   attrs      — JSONB of free-form metadata (icon, category hint,
--                visibility flags). Not the inner block tree.
--   content    — JSONB of the block tree itself (an array of root
--                Block nodes, the same shape that lives in
--                posts.content_blocks).
--   created_at — wall clock at insertion.
--   updated_at — wall clock at last edit. The renderer doesn't read
--                it; it backs the admin list view's "Last updated"
--                column.

CREATE TABLE reusable_blocks (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL
               CHECK (length(name) > 0 AND length(name) <= 256),
    attrs      JSONB NOT NULL DEFAULT '{}'::jsonb,
    content    JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- An index on name supports the inserter's "find by partial name"
-- lookup once the admin UI grows past a handful of entries. The
-- collation pin (default) is intentional — every other text index
-- in the project uses it.
CREATE INDEX reusable_blocks_name_idx ON reusable_blocks (name);

-- The renderer hits this table by primary key, but cron-style sweeps
-- (e.g. "delete unreferenced reusable blocks") want a created_at
-- sort.
CREATE INDEX reusable_blocks_created_idx ON reusable_blocks (created_at DESC);

COMMENT ON TABLE reusable_blocks IS
    'Named block-tree snippets referenced by core/block. See packages/go/blocks/reusable for the read/write path and apps/api/internal/admin/blocks for the admin CRUD.';
COMMENT ON COLUMN reusable_blocks.attrs IS
    'Free-form metadata for the entry (icon hint, category, visibility). Not the block tree.';
COMMENT ON COLUMN reusable_blocks.content IS
    'The block tree itself, mirroring posts.content_blocks. An array of root Block nodes (TypeScript: BlockTree).';
