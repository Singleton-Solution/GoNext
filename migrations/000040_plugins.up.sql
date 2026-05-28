-- 000040_plugins.up.sql
--
-- The `plugins` table: lifecycle row for every plugin known to the
-- platform. One row per installed plugin (`gn-seo`, `gn-redirects`, …)
-- carrying the State the lifecycle Manager has parked it in and the
-- raw manifest the bundle shipped with.
--
-- The Go implementation has lived in
-- `packages/go/plugins/lifecycle/postgres.go` since Wave N landed; the
-- column contract is documented in that package's `doc.go`. Until this
-- migration the table never actually existed: PR #527 papered over
-- this by making `Storage.List` return an empty slice on SQLSTATE
-- 42P01 (undefined_table) so the admin sidebar's
-- /api/v1/admin/plugin-pages poll didn't 500 on a fresh database, but
-- the other CRUD paths (Get/Insert/UpdateState/Delete) would still
-- explode. PR #529 mounted /api/v1/plugins on top of PostgresStorage,
-- which made the missing table user-visible the moment anyone hit a
-- non-List endpoint.
--
-- This migration creates the table the Go layer has been writing
-- against in spec, plus the state index documented in doc.go, plus the
-- BEFORE UPDATE trigger that keeps `updated_at` honest without making
-- the application layer remember to set it on every write.
--
-- Depends on:
--   * 000001_init — for the `citext` extension (used for the slug PK).
--   * 000004_posts — for the shared `touch_updated_at()` trigger
--                    helper. The helper is defined in 000004 and we
--                    reuse it here rather than declaring a new copy.

-- =============================================================================
-- Table
-- =============================================================================
--
-- Columns match the Storage interface in
-- packages/go/plugins/lifecycle/postgres.go::insertSQL / selectColumns:
--
--   slug          — natural PK. CITEXT so a future admin renaming via
--                   capital letters doesn't sneak a duplicate past the
--                   uniqueness check; the lifecycle Manager still
--                   validates the manifest-side regex
--                   (`^[a-z][a-z0-9-]{2,40}$`) before inserting so the
--                   on-disk values stay lower-case in practice.
--   version       — SemVer string from the manifest. Stored verbatim;
--                   the package does not compare versions.
--   abi_version   — host ABI the bundle was compiled against, so the
--                   admin UI can render compatibility info without
--                   re-parsing the manifest.
--   manifest      — raw manifest.json bytes.
--   state         — lifecycle State enum. CHECK constraint keeps a
--                   forgotten Storage caller from poisoning the column
--                   with a value the State type wouldn't accept.
--   capabilities  — sandbox capability list parsed out of the manifest's
--                   top-level `capabilities` block. Stored as JSONB so
--                   policy code can `?` / `@>` against it without
--                   re-parsing the manifest.
--   last_error    — most recent transition failure, human-readable.
--                   Cleared by Manager.Reset.
--   error_at      — moment `last_error` was recorded. NULL when there
--                   has been no error.
--   installed_at  — moment Install succeeded. Never updated after
--                   insert.
--   activated_at  — moment of the most recent Activate, or NULL if the
--                   plugin has never been activated. Not cleared on
--                   Deactivate so the admin UI can show "last active 3
--                   days ago".
--   row_version   — bumped on every UpdateState. Storage maintains it
--                   inside the UPDATE itself (see updateStateSQL in
--                   postgres.go), so the trigger here only touches
--                   updated_at.
--   updated_at    — bumped on every UPDATE by the trigger below.

CREATE TABLE plugins (
    slug            CITEXT PRIMARY KEY
                    CHECK (length(slug) > 0 AND length(slug) <= 64),

    version         TEXT NOT NULL,

    abi_version     INTEGER NOT NULL,

    manifest        JSONB NOT NULL DEFAULT '{}'::JSONB,

    state           TEXT NOT NULL
                    CHECK (state IN (
                        'installed',
                        'active',
                        'inactive',
                        'pending_uninstall',
                        'errored'
                    )),

    capabilities    JSONB NOT NULL DEFAULT '[]'::JSONB,

    last_error      TEXT NOT NULL DEFAULT '',
    error_at        TIMESTAMPTZ,

    installed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    activated_at    TIMESTAMPTZ,

    row_version     BIGINT NOT NULL DEFAULT 1,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE plugins IS
    'Plugin lifecycle rows. One per installed plugin. See packages/go/plugins/lifecycle/doc.go for the column contract.';
COMMENT ON COLUMN plugins.slug IS
    'Manifest-supplied identifier (regex ^[a-z][a-z0-9-]{2,40}$). CITEXT so case variants collide.';
COMMENT ON COLUMN plugins.state IS
    'Lifecycle State enum. CHECK enforces the same set the Go State type considers Valid().';
COMMENT ON COLUMN plugins.row_version IS
    'Optimistic-concurrency counter. Bumped inside Storage.UpdateState (see postgres.go::updateStateSQL).';

-- =============================================================================
-- Index: state lookups
-- =============================================================================
--
-- The admin sidebar and the future marketplace UI both filter by state
-- ("show me all Active plugins", "show me anything in Errored").
-- Without an index that's a sequential scan; the table is small but
-- the queries are on the render path, so an index is cheap insurance.
CREATE INDEX plugins_state_idx ON plugins (state);

-- =============================================================================
-- updated_at trigger
-- =============================================================================
--
-- Reuses the shared `touch_updated_at()` helper introduced in
-- 000004_posts.up.sql rather than declaring a per-table function — the
-- helper is exactly the body we need (NEW.updated_at := now();) and
-- has been the platform-wide convention for tables that only need
-- updated_at-bumping without an OCC counter (the OCC `row_version`
-- column here is managed by Storage.UpdateState in the UPDATE
-- statement itself, not by a trigger).
CREATE TRIGGER plugins_touch_updated_at
    BEFORE UPDATE ON plugins
    FOR EACH ROW
    EXECUTE FUNCTION touch_updated_at();
