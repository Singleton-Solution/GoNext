-- 000046_menus.up.sql
--
-- Navigation menus — issue #54. Two tables, modelled after the
-- WordPress nav-menus surface but with an ltree-style path column so
-- nested menus (e.g. mega-menus with two levels of children) can be
-- pulled with a single range scan instead of N round-trips.
--
-- A `menu` is a named container ("Primary", "Footer", "Mobile"); a
-- `menu_item` is a single link in it. Items carry a `path` of the form
-- "001", "001.002", "001.002.003" giving a stable in-tree position. The
-- renderer pulls every item for a menu_id ordered by path and rebuilds
-- the tree client-side; admin drag-to-reorder rewrites the path column
-- in a single transaction.
--
-- The `path` is TEXT rather than the postgres ltree type because we
-- don't want every dev environment to install the contrib extension —
-- the access pattern (prefix-match for descendants, order-by for full
-- listing) works fine on a plain TEXT column with a btree index, and
-- the contention with the ltree operator surface is zero.

CREATE TABLE menus (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Stable slug used by themes / the Navigation block to look up
    -- the menu by name without hard-coding the UUID. Unique.
    slug        TEXT NOT NULL UNIQUE
                CHECK (length(slug) > 0 AND length(slug) <= 64
                       AND slug ~ '^[a-z0-9][a-z0-9_-]*$'),
    -- Human label shown in the admin nav-menus index.
    name        TEXT NOT NULL
                CHECK (length(name) > 0 AND length(name) <= 128),
    -- Free-form metadata (theme-location hint, description). Not the
    -- items list — those live in menu_items.
    attrs       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE menus IS
    'Navigation menu containers. See packages/go/menus for the read/write path.';

CREATE TABLE menu_items (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    menu_id      UUID NOT NULL
                 REFERENCES menus(id) ON DELETE CASCADE,
    -- Dot-separated ltree-style path. Examples:
    --   "001"           — first root item
    --   "001.001"       — first child of "001"
    --   "001.001.001"   — first grandchild
    -- Sort order is lexicographic over the column, which keeps siblings
    -- next to each other and parents before children for a single
    -- ORDER BY path.
    path         TEXT NOT NULL
                 CHECK (length(path) > 0 AND length(path) <= 256
                        AND path ~ '^[0-9]{3}(\.[0-9]{3})*$'),
    -- Display label rendered as the link's text.
    label        TEXT NOT NULL
                 CHECK (length(label) > 0 AND length(label) <= 256),
    -- URL or path the link points at. Relative paths are resolved by
    -- the renderer; external URLs pass through unchanged.
    url          TEXT NOT NULL DEFAULT ''
                 CHECK (length(url) <= 2048),
    -- Optional reference to an internal object (post_id, page_id,
    -- taxonomy_term_id). The Navigation block prefers this over `url`
    -- so the link survives slug renames.
    object_type  TEXT
                 CHECK (object_type IS NULL OR
                        object_type = ANY(ARRAY['post','page','term','custom'])),
    object_id    UUID,
    -- Free-form per-item metadata (icon, target=_blank, css_class).
    attrs        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (menu_id, path)
);

COMMENT ON TABLE menu_items IS
    'Individual links inside a navigation menu. path is an ltree-style dot path; ORDER BY path returns parents before children.';

-- The hot path is "give me every item in menu X ordered for render".
-- A composite index on (menu_id, path) supports both the list and the
-- prefix-match used by "load this subtree only".
CREATE INDEX menu_items_menu_path_idx ON menu_items (menu_id, path);

-- updated_at trigger — inline rather than the shared helper for the
-- same reason the options migration inlined its own (no migration
-- ordering between this file and 000039_posts).
CREATE OR REPLACE FUNCTION menus_touch() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER menus_touch
    BEFORE UPDATE ON menus
    FOR EACH ROW
    EXECUTE FUNCTION menus_touch();

CREATE TRIGGER menu_items_touch
    BEFORE UPDATE ON menu_items
    FOR EACH ROW
    EXECUTE FUNCTION menus_touch();
