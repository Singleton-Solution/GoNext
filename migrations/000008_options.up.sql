-- 000008_options.up.sql
--
-- The `options` table: site-wide key/value configuration. Replaces
-- WordPress's `wp_options` (docs/01-core-cms.md §10.11) and explicitly
-- fixes the "every plugin autoloads everything" pathology that makes
-- wp_options the #1 cold-start cost on a typical WP install.
--
-- Three deliberate departures from wp_options:
--
--   1. `value` is JSONB, not TEXT. Typed values, not "true" / "1" /
--      "yes" stringly-typed booleans. Lookups can drill into nested
--      structure with `value -> 'inner' -> 'leaf'`.
--   2. `autoload` defaults to FALSE. In WP it's the other way around
--      and every plugin happily flips it on for itself, so a fresh
--      install with twenty plugins boots with 5k+ autoloaded rows.
--      Here you have to opt in.
--   3. `is_protected` marks rows the UI/plugin layer must not write
--      (e.g. core settings whose schema is enforced by application
--      code). Surface read-only badge in admin, refuse writes from
--      anything that isn't the core writer.
--
-- The table is independent of every other table in the schema — the
-- only dependency is `gen_uuid_v7()` from 000001 (not actually used
-- here, but kept consistent with the rest of the migration set in
-- requiring 000001 to be present first). No FKs, no joins, no
-- ordering between this migration and #39/#48/#55/etc.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE options (
    -- citext so that lookups for `Core.Site.Name` and `core.site.name`
    -- collide. Convention in code is lower-case dotted namespaces
    -- (`core.site.name`, `wpc-seo.sitemap.frequency`), but humans typing
    -- into an admin UI will get the case wrong eventually; citext
    -- absorbs that without forcing every read site to LOWER() the key.
    key             CITEXT PRIMARY KEY,

    -- JSONB so values keep their type. Strings, numbers, booleans,
    -- nested objects all round-trip cleanly. NOT NULL because there's
    -- no semantic difference between "missing key" and "key present
    -- but null" — if the row exists, it carries data.
    value           JSONB NOT NULL,

    -- FALSE by default. The autoload set is the hot-path bundle loaded
    -- into Redis at boot; growing it has a measurable cost. Anything
    -- that wants to be in that set has to ask for it explicitly,
    -- ideally via a plugin manifest the admin reviews at install time.
    autoload        BOOLEAN NOT NULL DEFAULT FALSE,

    -- TRUE marks the row as read-only from plugin/UI write paths.
    -- Core settings (default role, site URL when locked to env, etc.)
    -- live here; the application enforces the readonly contract.
    is_protected    BOOLEAN NOT NULL DEFAULT FALSE,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Optimistic concurrency. UPDATE ... WHERE key=$1 AND version=$2
    -- catches concurrent admin saves cleanly. Bumped by trigger below.
    version         INTEGER NOT NULL DEFAULT 1
);

COMMENT ON TABLE options IS
    'Site-wide key/value configuration. Replaces wp_options with typed JSONB values and opt-in autoload.';
COMMENT ON COLUMN options.autoload IS
    'TRUE = include in the boot-time Redis autoload hash. Defaults to FALSE; flipping it on is an explicit decision.';
COMMENT ON COLUMN options.is_protected IS
    'TRUE = readonly from plugin/UI write paths. Core writer is the only path allowed to mutate.';
COMMENT ON COLUMN options.version IS
    'Optimistic-concurrency counter. Maintained by trigger; bumped on every UPDATE.';

-- =============================================================================
-- Partial index for the boot-time "load all autoload rows" scan
-- =============================================================================
--
-- Boot path runs `SELECT key, value FROM options WHERE autoload = TRUE`
-- exactly once per process and hashes the result into Redis. A partial
-- index keeps that scan O(autoload-set-size) regardless of how many
-- non-autoload rows accumulate over the lifetime of the install — the
-- whole point of moving the autoload default to FALSE is that the
-- non-autoload set will dwarf the autoload set, and we want this index
-- to stay tiny.
CREATE INDEX options_autoload_idx ON options (key) WHERE autoload = TRUE;

-- =============================================================================
-- updated_at + version trigger
-- =============================================================================
--
-- Inlined rather than reusing a shared `touch_updated_at` /
-- `bump_version` pair (docs/01-core-cms.md §10.14) because those
-- helpers land with the posts migration (#39) and we don't want a
-- migration ordering dependency for what's a six-line function.
-- The posts migration can switch its triggers to the shared helpers
-- without touching ours.
CREATE OR REPLACE FUNCTION options_touch() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    NEW.version := OLD.version + 1;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER options_touch
    BEFORE UPDATE ON options
    FOR EACH ROW
    EXECUTE FUNCTION options_touch();

-- =============================================================================
-- Seed: core options
-- =============================================================================
--
-- These are the minimum keys the application reads at boot. Values are
-- placeholders for a fresh install; real values are written by the
-- installer / first-run wizard. Seeding them up front means
-- `Options.Get("core.site.name")` never returns ErrNotFound on a
-- freshly migrated database, which keeps the boot path branch-free.
--
-- `core.site.default_role` is marked protected: assigning the wrong
-- role to new sign-ups is a security incident, so we keep the column
-- behind the core writer.

INSERT INTO options (key, value, autoload, is_protected) VALUES
    ('core.site.name',           '"GoNext Site"'::jsonb,            TRUE, FALSE),
    ('core.site.tagline',        '"Built with GoNext"'::jsonb,      TRUE, FALSE),
    ('core.site.url',            '"http://localhost:8080"'::jsonb,  TRUE, FALSE),
    ('core.site.default_role',   '"subscriber"'::jsonb,             TRUE, TRUE),
    ('core.timezone',            '"UTC"'::jsonb,                    TRUE, FALSE),
    ('core.locale',              '"en"'::jsonb,                     TRUE, FALSE),
    ('core.permalinks.format',   '"/{year}/{month}/{slug}"'::jsonb, TRUE, FALSE);
