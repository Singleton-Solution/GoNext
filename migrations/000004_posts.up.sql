-- 000004_posts.up.sql
--
-- The core content table. Every piece of user-authored content in the
-- CMS lives here: posts, pages, attachments, navigation menu items,
-- and any plugin-defined custom post type (CPT). The discriminator is
-- the `post_type` column, which references `post_types.slug` only at
-- the application layer (see below).
--
-- This migration depends on:
--   * 000001_init — pgcrypto / citext extensions, `gen_uuid_v7()`,
--     and the `post_status` enum.
--   * 000002_users — `users(id)` for the author FK.
--   * 000003_post_types — `post_types(slug)` for the type registry
--     (referenced from app code, not a hard FK; see comment below).
--
-- Reference: docs/01-core-cms.md §10.5 and ADR 0008 (block tree
-- storage). The columns here are a superset of doc §10.5 — they
-- include the WordPress-compat fields (password protection, ping
-- status) and the canonicalised-hash column we use for the pre-render
-- cache key.

-- =============================================================================
-- Helper functions (idempotent)
-- =============================================================================
--
-- These two trigger helpers are referenced by *many* tables (posts,
-- users, comments, options, terms, …). They live in this migration —
-- the first one that needs them — and are created with CREATE OR
-- REPLACE so subsequent migrations can repeat the definition harmlessly
-- if it makes the file easier to read.
--
-- See doc 01 §10.14 for the full list of tables that wire these up.

-- Bump updated_at to wall-clock time on every UPDATE.
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION touch_updated_at() IS
    'BEFORE UPDATE trigger helper: sets NEW.updated_at = now(). Wired up on every table that exposes an updated_at column.';

-- Optimistic concurrency: every UPDATE bumps `version` by one. The API
-- layer issues UPDATEs with `WHERE id = $1 AND version = $2` so a
-- concurrent writer reliably gets zero rows back instead of stomping
-- on the other writer's change.
CREATE OR REPLACE FUNCTION bump_version() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.version = OLD.version + 1;
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION bump_version() IS
    'BEFORE UPDATE trigger helper: NEW.version = OLD.version + 1. Used by every table that participates in optimistic-concurrency UPDATEs.';

-- =============================================================================
-- posts
-- =============================================================================

CREATE TABLE posts (
    -- Primary key. UUID v7 keeps inserts B-tree-friendly (ADR 0003).
    id                   UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Type discriminator. References `post_types.slug` semantically,
    -- but is intentionally *not* a hard FK. Plugins may register a
    -- new CPT slug at boot time, and the row in `post_types` may not
    -- yet exist at the moment a plugin's seed migration inserts its
    -- first post (chicken-and-egg). The app enforces validity at the
    -- write path; the cost of a stale slug is a 404, not corruption.
    -- Stored as plain text (not citext) because slugs in `post_types`
    -- are citext but URL routing is byte-exact — we lowercase on
    -- insert from the app side.
    post_type            TEXT NOT NULL,

    -- Hierarchical parent for types that nest (page, nav menu item).
    -- ON DELETE SET NULL so deleting a parent leaves orphans visible
    -- in the admin where they can be re-parented or trashed manually;
    -- cascading deletes would silently destroy entire subtrees.
    parent_id            UUID REFERENCES posts(id) ON DELETE SET NULL,

    -- Author. RESTRICT so we never silently lose authorship records:
    -- to delete a user, the admin must first re-assign or trash that
    -- user's posts (see doc 01 §6.3 on author reassignment).
    author_id            UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

    -- Publishing state. Uses the enum from 000001 so the set of
    -- allowed values is enforced at the type system.
    status               post_status NOT NULL DEFAULT 'draft',

    -- Display title. Empty string is a valid title (used by
    -- attachments and some CPTs that store the user-visible label in
    -- meta), so NOT NULL with default '' rather than nullable.
    title                TEXT NOT NULL DEFAULT '',

    -- URL slug. citext so case-insensitive uniqueness is enforced by
    -- the partial uniques below without us having to remember to
    -- LOWER() on every comparison.
    slug                 CITEXT NOT NULL,

    -- Short summary. Nullable: many post types have no concept of an
    -- excerpt (attachments, nav items), and an empty string would
    -- carry different semantics from "not set" in the admin UI.
    excerpt              TEXT,

    -- Block tree (ADR 0008). The canonical content store. NOT NULL
    -- with default '[]' so every row has a queryable JSONB document
    -- and we don't litter app code with NULL checks.
    content_blocks       JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Hash of the canonicalised block tree at the time
    -- content_rendered was produced. Used as the cache key for the
    -- pre-render cache (doc 04 §1.4 / §5.5). bytea so we can store
    -- the raw bytes of a SHA-256 (or whatever algorithm we pick)
    -- without hex-encoding overhead. Nullable: a row exists before
    -- it's ever rendered.
    content_blocks_hash  BYTEA,

    -- Pre-rendered HTML cache. Rebuilt by the renderer on save (or
    -- on demand if content_blocks_hash mismatches). Nullable for
    -- unrendered drafts.
    content_rendered     TEXT,
    content_rendered_at  TIMESTAMPTZ,

    -- Post-level password protection. Matches WordPress: a non-NULL
    -- password gates the rendered output behind a cookie challenge.
    -- Stored hashed by the application layer (see doc 06 §4); the
    -- column type is TEXT so we have room for whichever hash format
    -- we settle on (likely argon2id).
    password             TEXT,

    -- Comments toggle. WordPress compat: 'open' allows new comments,
    -- 'closed' freezes the thread. CHECK constraint keeps the set
    -- closed without inventing yet another enum.
    comment_status       TEXT NOT NULL DEFAULT 'open'
                         CHECK (comment_status IN ('open', 'closed')),

    -- Pingback / trackback toggle. Defaulted to 'closed' because the
    -- pingback protocol is essentially dead and a spam magnet; we
    -- keep the column for WP-import fidelity.
    ping_status          TEXT NOT NULL DEFAULT 'closed'
                         CHECK (ping_status IN ('open', 'closed')),

    -- Ordering within a parent. Used by hierarchical types (pages,
    -- nav menu items) to control sibling order.
    menu_order           INTEGER NOT NULL DEFAULT 0,

    -- Plugin-extensible metadata bag. Keys must be namespaced by
    -- plugin slug per doc 01 §3 (e.g. `seo.title`, `acme.featured`).
    -- The GIN index below uses jsonb_path_ops because we only ever
    -- query with the `@>` containment operator.
    meta                 JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- Set when status flips to 'published'. Preserved across
    -- subsequent re-publishes (drafts of published posts get their
    -- original published_at back on re-publish) so canonical URLs
    -- with the publication date in them remain stable.
    published_at         TIMESTAMPTZ,

    -- Set when status is 'scheduled'. The cron worker (doc 12)
    -- scans this column for rows whose time has come and flips
    -- them to 'published'.
    scheduled_for        TIMESTAMPTZ,

    -- Lifecycle. created_at is immutable; updated_at is maintained
    -- by trigger.
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Optimistic concurrency token. Bumped by trigger on every
    -- UPDATE. The API issues UPDATEs as
    --     UPDATE posts SET … WHERE id = $1 AND version = $2
    -- and treats a 0-row result as a stale-write conflict.
    version              INTEGER NOT NULL DEFAULT 1
);

COMMENT ON TABLE posts IS
    'Core content table. Holds posts, pages, attachments, nav items, and any plugin-defined CPT. Discriminated by post_type.';

COMMENT ON COLUMN posts.post_type IS
    'Slug of the post type. References post_types.slug at the app layer (not a hard FK so plugins can register slugs at boot).';

COMMENT ON COLUMN posts.content_blocks IS
    'JSON block tree (ADR 0008). The canonical content store; content_rendered is a cache derived from this.';

COMMENT ON COLUMN posts.content_blocks_hash IS
    'Hash of the canonicalised content_blocks at the time content_rendered was produced. Cache key for the pre-render cache.';

COMMENT ON COLUMN posts.published_at IS
    'Set when status first flips to published, preserved across re-publish so canonical date-based URLs stay stable.';

COMMENT ON COLUMN posts.scheduled_for IS
    'Target publish time while status = scheduled. The cron worker flips eligible rows to published.';

COMMENT ON COLUMN posts.version IS
    'Optimistic-concurrency token. Bumped by trigger on every UPDATE; clients pass the expected value in WHERE.';

-- =============================================================================
-- Triggers
-- =============================================================================
--
-- One BEFORE UPDATE trigger bumps both updated_at and version. We
-- attach two separate triggers so each helper function stays focused
-- on a single column — easier to reason about and easier to share
-- with the other tables that will reuse them.

CREATE TRIGGER posts_touch_updated_at
    BEFORE UPDATE ON posts
    FOR EACH ROW
    EXECUTE FUNCTION touch_updated_at();

CREATE TRIGGER posts_bump_version
    BEFORE UPDATE ON posts
    FOR EACH ROW
    EXECUTE FUNCTION bump_version();

-- =============================================================================
-- Indexes
-- =============================================================================
--
-- Slug uniqueness is partial: a slug must be unique within its type
-- (and within its parent, for hierarchical types), but only among
-- non-trash rows. A trashed post keeps its slug so it can be restored,
-- but a new post can claim the same slug without colliding.

-- Flat slugs: uniqueness per (type, slug) when there is no parent.
CREATE UNIQUE INDEX posts_slug_flat_uq
    ON posts (post_type, slug)
    WHERE parent_id IS NULL AND status <> 'trash';

-- Hierarchical slugs: uniqueness per (type, parent, slug) when there
-- is a parent. Two pages under different parents can share a slug
-- (`/about/team` and `/company/team` both have slug 'team').
CREATE UNIQUE INDEX posts_slug_hier_uq
    ON posts (post_type, parent_id, slug)
    WHERE parent_id IS NOT NULL AND status <> 'trash';

-- Archive queries: "give me the latest N published posts of type X."
-- DESC on published_at because archives display newest-first; the
-- planner can use this index for both filter and sort.
CREATE INDEX posts_type_status_published_idx
    ON posts (post_type, status, published_at DESC);

-- "Posts by author" queries. Cheap and small.
CREATE INDEX posts_author_idx ON posts (author_id);

-- FK lookups: when a parent is deleted (ON DELETE SET NULL) Postgres
-- needs an index on the referencing column to avoid a full scan.
CREATE INDEX posts_parent_idx ON posts (parent_id);

-- Plugin/meta containment queries. jsonb_path_ops is the smaller,
-- faster GIN opclass that only supports the `@>` operator — which is
-- exactly what meta queries use ("find posts whose meta contains
-- {seo:{noindex:true}}"). We add path-specific b-tree indexes per
-- plugin as they prove to be hot.
CREATE INDEX posts_meta_gin ON posts USING gin (meta jsonb_path_ops);
