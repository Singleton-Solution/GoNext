-- 000018_plugin_listings.up.sql
--
-- Marketplace data model — listings.
--
-- The plugin runtime (Waves D–G) ships a working installer that takes a
-- bundle from disk and lands it in the `plugins` table. That's enough
-- for a local CMS to side-load a plugin a developer hands over. It is
-- emphatically NOT enough for a community marketplace, where multiple
-- third parties publish, version, rate, and install plugins through a
-- shared catalogue.
--
-- This migration and its four siblings (000019–000022) put the
-- marketplace data model in place so that:
--
--   * Publishers can register a *listing* — the public, human-facing
--     identity of a plugin (slug, name, author, license, category).
--   * A listing can have many *versions* — the binary artefacts plus
--     their manifest, integrity hash, and optional signature.
--   * Each version can declare a *compatibility matrix* — the host
--     ABI ranges it's been tested against.
--   * Users can leave *ratings* — one per (version, user), 1–5 stars.
--   * The platform records *install events* — append-only telemetry
--     used by the future marketplace UI for popularity ranking.
--
-- The marketplace UI itself lands later (see the marketplace tracker
-- issue). This PR is data-model + Go store layer only.
--
-- Depends on:
--   * 000001_init — for gen_uuid_v7() and the pgcrypto extension.
--   * 000002_users — for the author_id FK target.

-- =============================================================================
-- plugin_listings
-- =============================================================================
--
-- One row per published-or-publishable plugin. The slug is the
-- public-facing handle that appears in URLs ("/marketplace/gn-seo")
-- and is the join key for every other marketplace table — it lives
-- alongside a UUID PK (per ADR 0003) rather than replacing it, because
-- slugs occasionally need to be renamed and we'd rather keep the FKs
-- pointing at a stable identifier.

CREATE TABLE plugin_listings (
    -- Time-sortable UUID v7. Matches the platform's PK convention so
    -- joins against `users` (also UUID v7) keep clustering behaviour
    -- predictable.
    id                  UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Public-facing handle. Lowercase + hyphens by convention; we don't
    -- enforce the shape in the column because the application layer
    -- already validates against the manifest schema before insert.
    -- UNIQUE so URL lookups can use it as a natural key.
    slug                TEXT NOT NULL UNIQUE
                        CHECK (length(slug) > 0 AND length(slug) <= 128),

    -- Human-facing display name. Distinct from slug so a listing can
    -- rebrand without breaking URLs. Required at insert time; an empty
    -- listing card would be useless.
    name                TEXT NOT NULL
                        CHECK (length(name) > 0 AND length(name) <= 256),

    -- One-line description for catalogue cards. Optional — a brand-new
    -- draft may not have copy yet.
    summary             TEXT,

    -- The publishing user. ON DELETE SET NULL so deleting a user
    -- preserves the catalogue (the listing becomes "unowned" and the
    -- moderation team can re-assign or delist it).
    author_id           UUID
                        REFERENCES users(id) ON DELETE SET NULL,

    -- Optional project page. Plain text rather than a constrained URL
    -- type because the validation surface lives in the application.
    homepage_url        TEXT,

    -- SPDX licence identifier ("MIT", "Apache-2.0", "GPL-3.0-only", …).
    -- Stored as opaque text — the SPDX catalogue is large and evolves
    -- faster than schema migrations.
    license_spdx        TEXT,

    -- Primary category for catalogue browsing ("seo", "analytics",
    -- "editor-extension", …). Free-form text rather than an enum so
    -- new categories can be introduced without a schema change.
    primary_category    TEXT,

    -- Lifecycle:
    --   draft     — owner is still preparing the listing; not visible
    --               in the catalogue.
    --   listed    — visible in the catalogue, installable.
    --   delisted  — temporarily hidden by the owner or platform; the
    --               existing installs continue to work but no new
    --               discovery happens.
    --   banned    — permanent moderation action. Distinct from delisted
    --               so the audit trail records intent.
    status              TEXT NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft','listed','delisted','banned')),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE  plugin_listings IS
    'Public-facing plugin catalogue row. Owns the slug, name, author, and lifecycle status.';
COMMENT ON COLUMN plugin_listings.slug             IS 'Public handle, unique. Appears in marketplace URLs.';
COMMENT ON COLUMN plugin_listings.status           IS 'Lifecycle: draft | listed | delisted | banned.';
COMMENT ON COLUMN plugin_listings.primary_category IS 'Free-form category string for catalogue browsing; not an enum.';

-- Browsing the catalogue by category is the dominant read pattern;
-- the partial index covers only listed rows because draft/delisted/
-- banned are out of the catalogue's main view.
CREATE INDEX plugin_listings_category_idx
    ON plugin_listings (primary_category)
    WHERE status = 'listed';

-- "Show me everything by this author" — feeds the publisher dashboard.
CREATE INDEX plugin_listings_author_idx
    ON plugin_listings (author_id)
    WHERE author_id IS NOT NULL;

-- updated_at touch trigger. We keep a marketplace-local trigger
-- function rather than reusing touch_updated_at_and_version() because
-- the listing row doesn't carry a `version` column (the OCC token only
-- matters for tables that admins read-modify-write through the API; the
-- marketplace store does plain UPDATEs).
CREATE OR REPLACE FUNCTION marketplace_touch_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION marketplace_touch_updated_at() IS
    'BEFORE UPDATE trigger body for marketplace tables that need updated_at but not a version counter.';

CREATE TRIGGER plugin_listings_touch_updated_at
    BEFORE UPDATE ON plugin_listings
    FOR EACH ROW
    EXECUTE FUNCTION marketplace_touch_updated_at();
