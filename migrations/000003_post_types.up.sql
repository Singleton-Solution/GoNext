-- 000003_post_types.up.sql
--
-- The post_types registry: one row per content type in the system
-- (built-in `post` and `page`, plus anything plugins / themes
-- register at runtime). See docs/01-core-cms.md §1.3 for the
-- conceptual model and §10.4 for the canonical DDL this file
-- implements.
--
-- Why a database registry and not a code-only one?
-- -------------------------------------------------
-- The in-memory registry (the Go `PostType` struct in §1.3) is what
-- the runtime actually consults to render the editor, mount routes,
-- and check capabilities. The table here exists so that:
--
--   1. Foreign keys can point at it (`posts.type REFERENCES
--      post_types(slug)` — wired in #55).
--   2. CPTs declared by plugins survive a process restart without
--      needing every plugin to re-register on every boot.
--   3. Admin UIs can list the available types with a plain SELECT.
--
-- The runtime is still the source of truth for behaviour; this table
-- is the source of truth for "which types exist right now in this
-- install".
--
-- Dependencies: 000001 (gen_uuid_v7, citext). This file does not
-- reference users(id) and so is independent of 000002 at the file
-- level; the migration runner applies them in numeric order.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE post_types (
    -- UUID v7 primary key per ADR 0003. The `slug` column below is the
    -- human-friendly identifier used in FKs from `posts.type` and in
    -- URLs / plugin manifests; the surrogate UUID buys us free renames
    -- and a stable identity for audit logs.
    id UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Machine-readable slug. citext so we don't accidentally register
    -- both "Post" and "post" — case-insensitive uniqueness is what
    -- WordPress users expect from custom-post-type slugs.
    slug citext NOT NULL UNIQUE,

    -- Display name, shown in the admin UI. Singular form.
    name text NOT NULL,

    -- Pluralised display name. We store it explicitly rather than
    -- relying on a string-munge pluraliser because non-English locales
    -- need to ship their own labels and "post" -> "posts" is a poor
    -- heuristic for languages that aren't English.
    name_plural text NOT NULL,

    -- Hierarchical types have `parent_id` semantics on rows in the
    -- `posts` table (pages, nav menu items). Flat types (post, most
    -- CPTs) ignore the parent column.
    hierarchical boolean NOT NULL DEFAULT false,

    -- Public types render archive / single pages on the front-end and
    -- show up in default REST listings. Internal types (revisions,
    -- some plugin bookkeeping types) flip this off.
    public boolean NOT NULL DEFAULT true,

    -- Routable types are reachable by URL. A type can be `public`
    -- (visible in admin search, listable via API) but not `routable`
    -- (no permalink). Separating the two avoids the WP-era trick of
    -- registering a CPT with public=false just to suppress permalinks.
    routable boolean NOT NULL DEFAULT true,

    -- Feature flags this type opts into. Values are drawn from a
    -- well-known set: title, editor, excerpt, comments, revisions,
    -- author, thumbnail, custom-fields, page-attributes. The runtime
    -- validates the contents; we leave it loose in the DB so plugins
    -- that ship their own supports keys (e.g. `events:rsvp`) aren't
    -- locked out at the schema layer.
    supports text[] NOT NULL DEFAULT '{}',

    -- Block allow-list. NULL means "every registered block type is
    -- allowed"; a non-NULL array restricts the editor to the listed
    -- block-type globs (e.g. ARRAY['core/*', 'my-plugin/pricing']).
    -- See docs/01-core-cms.md §1.3 and docs/04-block-editor.md §2.4.
    supports_blocks text[],

    -- Capability family per contract S7. 'post' means "inherit the
    -- standard post family caps" (edit_posts, publish_posts, …). A
    -- new prefix (e.g. 'book') mints a fresh family — slugs are
    -- auto-derived from the prefix (edit_books, publish_books, …)
    -- unless overridden in `capabilities` below.
    capability_type text NOT NULL DEFAULT 'post',

    -- Explicit per-action overrides for the auto-derived capability
    -- slugs. Empty object = derive all from `capability_type`. See
    -- docs/01-core-cms.md §1.3 for the canonical mapping rule.
    capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- JSON Schema 2020-12 describing the typed shape of custom fields
    -- for this type. Validated by the API on write (see
    -- docs/01-core-cms.md §9). Empty object = no schema enforced.
    field_schema jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Optional companion ("sidecar") table name for CPTs that need
    -- real typed columns with indexes / FKs. NULL = no sidecar.
    -- The runtime resolves the SidecarStore implementation by this
    -- name; see §1.3.
    sidecar_table text,

    -- Lifecycle.
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Optimistic concurrency counter, bumped by the trigger below on
    -- every UPDATE. The API uses this in `UPDATE ... WHERE id = $1
    -- AND version = $2` to detect concurrent edits in the registry
    -- editor.
    version integer NOT NULL DEFAULT 1
);

COMMENT ON TABLE post_types IS
    'Registry of content types (built-in + plugin-registered). See docs/01-core-cms.md §1.3 / §10.4.';

-- =============================================================================
-- updated_at + version bump trigger
-- =============================================================================
--
-- Convention across the schema: any table with both `updated_at` and
-- `version` columns has a BEFORE UPDATE trigger that sets
-- `updated_at = now()` and `version = OLD.version + 1`.
--
-- We name the function with a `post_types_` prefix so that this file
-- can be applied independently of 000002 (users) — if 000002 ships a
-- general-purpose `bump_updated_at_and_version()`, that one keeps
-- working for `users` and ours coexists without clashing. When the
-- shared helper lands we can consolidate in a later migration.
CREATE OR REPLACE FUNCTION post_types_bump_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at := now();
    NEW.version   := OLD.version + 1;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION post_types_bump_updated_at() IS
    'BEFORE UPDATE trigger fn for post_types: bumps updated_at and version.';

CREATE TRIGGER post_types_bump_updated_at
    BEFORE UPDATE ON post_types
    FOR EACH ROW
    EXECUTE FUNCTION post_types_bump_updated_at();

-- =============================================================================
-- Built-in seeds
-- =============================================================================
--
-- The two cornerstone built-ins per docs/01-core-cms.md §1.4. Other
-- built-in types (attachment, revision, nav_menu_item, block_pattern,
-- template, template_part) land with the migrations that introduce
-- their dependent infrastructure (media tables, FSE templates, etc.).
--
-- ON CONFLICT (slug) DO NOTHING makes this idempotent: re-running
-- 000003 against a database where the seeds already exist is a no-op
-- rather than a constraint violation, which matters because operators
-- sometimes replay migrations during DR rehearsals.

INSERT INTO post_types (slug, name, name_plural, hierarchical, public, routable, supports)
VALUES
    ('post', 'Post', 'Posts', false, true, true,
     ARRAY['title','editor','excerpt','comments','revisions','author','thumbnail','custom-fields']),
    ('page', 'Page', 'Pages', true, true, true,
     ARRAY['title','editor','author','thumbnail','custom-fields','page-attributes'])
ON CONFLICT (slug) DO NOTHING;
