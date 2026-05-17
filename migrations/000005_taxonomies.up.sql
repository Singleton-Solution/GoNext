-- 000005_taxonomies.up.sql
--
-- Taxonomies, terms (with ltree materialized path), and the
-- post<->term join table. This collapses WordPress's
-- wp_terms / wp_term_taxonomy split into a single `terms` table
-- carrying the taxonomy as a column; see docs/01-core-cms.md §2 and
-- §10.7. The collapse is a deliberate improvement: WP's split paid
-- for itself only when terms were shared across taxonomies, which in
-- practice nobody does.
--
-- Layout:
--
--   taxonomies   — registry table (parallel to post_types). Built-in
--                  seeds: `category` (hierarchical) and `tag` (flat).
--   terms        — one row per term, with parent_id + ltree `path`
--                  for fast ancestor/descendant queries.
--   term_relationships
--                — many-to-many between posts and terms, with a
--                  `position` for ordered taxonomies ("primary
--                  category", featured tag).
--
-- Triggers:
--   * Maintain terms.path on parent_id INSERT/UPDATE (recurse
--     parent chain → label). On parent moves, rebuild the subtree.
--   * Maintain terms.count on term_relationships INSERT/DELETE.
--
-- Dependencies (assumed present from earlier migrations):
--   000001 → ltree, citext, gen_uuid_v7()
--   000004 → posts(id)

-- =============================================================================
-- taxonomies
-- =============================================================================
--
-- The registry. `slug` is the natural key used by code and by terms.taxonomy.
-- We keep a UUID `id` for joins from other tables (audit, plugins, etc.).
-- terms.taxonomy is enforced at the application layer rather than via a
-- DB FK — this lets plugin-supplied taxonomies be registered/unregistered
-- without a schema migration, while the seeds below pin the two built-ins.

CREATE TABLE taxonomies (
    id           UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    slug         citext NOT NULL UNIQUE,
    name         text NOT NULL,
    name_plural  text NOT NULL,
    hierarchical boolean NOT NULL DEFAULT false,
    public       boolean NOT NULL DEFAULT true,
    meta         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    version      integer NOT NULL DEFAULT 1
);

COMMENT ON TABLE  taxonomies IS 'Registry of taxonomy kinds (category, tag, …). One row per taxonomy.';
COMMENT ON COLUMN taxonomies.slug IS 'Natural key used by terms.taxonomy and by URL routes; citext for case-insensitive uniqueness.';
COMMENT ON COLUMN taxonomies.hierarchical IS 'True = tree (categories), false = flat (tags). Drives whether terms.parent_id is meaningful.';
COMMENT ON COLUMN taxonomies.public IS 'False hides the taxonomy from public archive routes and front-end queries.';

-- Built-in seeds. `category` is the canonical hierarchical taxonomy; `tag` is
-- the canonical flat taxonomy. Both ship with every install.
INSERT INTO taxonomies (slug, name, name_plural, hierarchical, public) VALUES
    ('category', 'Category', 'Categories', true,  true),
    ('tag',      'Tag',      'Tags',       false, true);

-- =============================================================================
-- terms
-- =============================================================================
--
-- One row per term. `taxonomy` is a plain text column (no FK) — the registry
-- enforcement lives in the app, but the partial unique index below means a
-- given (taxonomy, slug, parent) triple can only exist once.
--
-- Hierarchy:
--   * parent_id is a self-FK with ON DELETE SET NULL (so deleting a parent
--     promotes children to roots rather than orphaning them).
--   * path is a materialized ltree label chain ("tech.programming.go") kept
--     in sync by triggers below. Reading the whole subtree is a single
--     `WHERE path <@ 'tech'` against the GiST index — no recursive CTE.
--
-- Counts: `count` is denormalized for archive-listing performance (the
-- hottest read in most blogs is "posts in this term"). The trigger on
-- term_relationships keeps it accurate. See §2.4 of the design doc.

CREATE TABLE terms (
    id          UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    taxonomy    text NOT NULL,
    parent_id   UUID REFERENCES terms(id) ON DELETE SET NULL,
    path        ltree,
    slug        citext NOT NULL,
    name        text NOT NULL,
    description text,
    count       integer NOT NULL DEFAULT 0,
    meta        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    version     integer NOT NULL DEFAULT 1
);

COMMENT ON TABLE  terms IS 'Terms in a taxonomy. One row per (taxonomy, slug, parent). path is auto-maintained.';
COMMENT ON COLUMN terms.taxonomy IS 'Taxonomy slug from taxonomies.slug; not a hard FK so plugins can register taxonomies dynamically.';
COMMENT ON COLUMN terms.path IS 'Materialized ltree label chain from the root term to this one. Maintained by trigger; do not write directly.';
COMMENT ON COLUMN terms.count IS 'Denormalized count of attached posts. Maintained by trigger on term_relationships.';

-- Partial unique index on (taxonomy, slug, parent_id). We can't put NULL
-- directly into a UNIQUE constraint and get the "two NULLs collide" semantics
-- without using COALESCE to a sentinel UUID. The all-zeros UUID is a safe
-- sentinel — gen_uuid_v7() can never produce it (the timestamp bytes guarantee
-- the upper bits are non-zero).
CREATE UNIQUE INDEX terms_taxonomy_slug_parent_uq
    ON terms (taxonomy, slug, COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid));

-- GiST on path is the index that makes the whole ltree design pay off:
-- "all descendants of X" becomes `path <@ 'x'` in one index lookup.
CREATE INDEX terms_path_gist_idx ON terms USING gist (path);

-- Term-by-name lookup (admin search, autocomplete).
CREATE INDEX terms_taxonomy_name_idx ON terms (taxonomy, name);

-- =============================================================================
-- term_relationships
-- =============================================================================
--
-- Many-to-many between posts and terms. `position` exists because plugins
-- repeatedly reinvent ordering and "primary category" / "featured tag" can
-- be computed as `MIN(position)`.
--
-- The index on (term_id, position, post_id) supports the term-archive query
-- (§2.3) with `term_id` leading — that's the hot read path for /category/<slug>.

CREATE TABLE term_relationships (
    post_id  UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    term_id  UUID NOT NULL REFERENCES terms(id) ON DELETE CASCADE,
    position integer NOT NULL DEFAULT 0,
    PRIMARY KEY (post_id, term_id)
);

COMMENT ON TABLE term_relationships IS 'Post<->term assignments. PK (post_id, term_id); position orders multiple terms on one post.';

-- "Posts in this term" is the term archive query (the hottest read in most
-- blogs). term_id-leading order lets the planner use this as the driver in
-- an index nested loop with `posts(published_at DESC) WHERE status=…`.
CREATE INDEX term_relationships_term_idx
    ON term_relationships (term_id, position, post_id);

-- =============================================================================
-- Triggers: terms.path maintenance
-- =============================================================================
--
-- Idea: each term's path is the parent's path with the term's slug appended
-- as a single ltree label. Root terms (parent_id IS NULL) have path = <slug>.
--
-- We have to handle three events:
--   1. INSERT — compute path from the (possibly NULL) parent at row time.
--   2. UPDATE of parent_id — recompute this row's path.
--   3. UPDATE of slug — recompute this row's path *and* cascade to every
--      descendant whose path starts with the old prefix.
--   4. UPDATE of parent_id where the term has children — same cascade.
--
-- ltree labels must match `[A-Za-z0-9_]+`. Slugs are citext but in practice
-- contain `-` from kebab-case ("hello-world"). We map `-` → `_` for the
-- label form; the human-facing slug stays unchanged. (If a slug somehow
-- contains a `.` it would break ltree parsing — that's a slug-validation
-- problem belonging to the app, not the migration.)

CREATE OR REPLACE FUNCTION term_slug_to_label(slug text)
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT replace(lower(slug), '-', '_');
$$;

COMMENT ON FUNCTION term_slug_to_label(text) IS
    'Maps a citext slug into a valid ltree label (lowercase, dashes to underscores).';

-- compute_term_path: returns the full ltree path for a term given its
-- (parent_id, slug). Walks the parent chain by looking up parent.path
-- — which is already maintained by this same trigger family, so a single
-- lookup suffices (no recursion needed at the SQL level).
CREATE OR REPLACE FUNCTION compute_term_path(p_parent UUID, p_slug text)
RETURNS ltree
LANGUAGE plpgsql
STABLE
AS $$
DECLARE
    parent_path ltree;
BEGIN
    IF p_parent IS NULL THEN
        RETURN text2ltree(term_slug_to_label(p_slug));
    END IF;

    SELECT path INTO parent_path FROM terms WHERE id = p_parent;
    IF parent_path IS NULL THEN
        -- Parent exists but its path hasn't been set yet (shouldn't happen
        -- once the BEFORE trigger has run on every row). Fall back to a
        -- single-label path; the cascade will fix it up on the next update.
        RETURN text2ltree(term_slug_to_label(p_slug));
    END IF;

    RETURN parent_path || text2ltree(term_slug_to_label(p_slug));
END;
$$;

-- BEFORE INSERT / BEFORE UPDATE: set NEW.path from the parent's path.
CREATE OR REPLACE FUNCTION terms_set_path()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.path := compute_term_path(NEW.parent_id, NEW.slug);
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER terms_set_path_ins
    BEFORE INSERT ON terms
    FOR EACH ROW
    EXECUTE FUNCTION terms_set_path();

-- On UPDATE we only need to rewrite path if the parent or slug changed —
-- bumping the trigger on every column would be wasted work.
CREATE TRIGGER terms_set_path_upd
    BEFORE UPDATE OF parent_id, slug ON terms
    FOR EACH ROW
    WHEN (
        NEW.parent_id IS DISTINCT FROM OLD.parent_id
     OR NEW.slug      IS DISTINCT FROM OLD.slug
    )
    EXECUTE FUNCTION terms_set_path();

-- AFTER UPDATE: if this row's path actually changed, cascade to descendants.
-- We replace the old prefix with the new prefix on every row whose path is
-- a descendant of the OLD path. ltree supplies the `<@` test and the
-- `subpath`/concatenation operators that make this a single UPDATE.
CREATE OR REPLACE FUNCTION terms_cascade_path()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.path IS DISTINCT FROM OLD.path AND OLD.path IS NOT NULL THEN
        UPDATE terms
           SET path = NEW.path || subpath(terms.path, nlevel(OLD.path)),
               updated_at = now()
         WHERE terms.id <> NEW.id
           AND terms.path <@ OLD.path;
    END IF;
    RETURN NULL;
END;
$$;

-- Note on trigger column lists: an `AFTER UPDATE OF path` trigger only fires
-- when `path` appears in the SET clause of the UPDATE — assignments made by
-- BEFORE triggers don't count. We instead fire on parent_id / slug (the
-- columns the caller actually writes) and gate on path having changed; that
-- way a parent move correctly cascades to descendants.
CREATE TRIGGER terms_cascade_path_upd
    AFTER UPDATE OF parent_id, slug ON terms
    FOR EACH ROW
    WHEN (NEW.path IS DISTINCT FROM OLD.path)
    EXECUTE FUNCTION terms_cascade_path();

-- =============================================================================
-- Triggers: terms.count maintenance
-- =============================================================================
--
-- A row added to term_relationships bumps the term's count; a row removed
-- decrements it. We don't bother with UPDATE (the PK can't change without
-- a delete+insert).
--
-- We do NOT also touch the post side here — `posts.status` transitions
-- between draft/published also affect counts (a draft doesn't count). That
-- logic lives in the posts migration's status trigger (issue #55) so the
-- relationship table only owns the +1/-1 on attach/detach.

CREATE OR REPLACE FUNCTION recount_terms_on_rel_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE terms SET count = count + 1, updated_at = now()
         WHERE id = NEW.term_id;
        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE terms SET count = GREATEST(count - 1, 0), updated_at = now()
         WHERE id = OLD.term_id;
        RETURN OLD;
    END IF;
    RETURN NULL;
END;
$$;

COMMENT ON FUNCTION recount_terms_on_rel_change() IS
    'Maintain terms.count on attach/detach. Status-driven count changes belong to the posts trigger.';

CREATE TRIGGER term_relationships_recount_ins
    AFTER INSERT ON term_relationships
    FOR EACH ROW
    EXECUTE FUNCTION recount_terms_on_rel_change();

CREATE TRIGGER term_relationships_recount_del
    AFTER DELETE ON term_relationships
    FOR EACH ROW
    EXECUTE FUNCTION recount_terms_on_rel_change();
