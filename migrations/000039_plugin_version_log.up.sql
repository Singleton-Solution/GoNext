-- 000039_plugin_version_log.up.sql
--
-- Versioned-update tracking for the plugin lifecycle (issue #63).
--
-- The plugins table holds a single row per slug — the *current*
-- active version. When the operator rolls out a new version, the
-- lifecycle Manager:
--
--   1. Loads the new bundle's WASM into the runtime side-by-side
--      with the previous version.
--   2. Records the new version in this table as 'active' and flips
--      the previous row to 'retiring' atomically.
--   3. Drains in-flight requests against the previous version (poll
--      on the in-process drainTracker; default 30s timeout).
--   4. Marks the previous row 'retained' with a retention_end of
--      now + 24h so a rollback is a cheap promote.
--   5. A cron job calls PurgeExpired which deletes rows whose
--      retention_end < now and any rows already marked 'retired'.
--
-- The previous marketplace table `plugin_versions` (000019) tracks
-- *published* versions in the catalog — distinct from this table,
-- which tracks the *installed* version log on a specific host. We
-- name this table plugin_version_log to avoid the collision.
--
-- Depends on:
--   * the runtime plugins table (referenced by slug FK only — the
--     FK is intentionally NOT declared because we want the version
--     log to survive an Uninstall + Reinstall cycle for audit
--     purposes; the cleanup cron deletes orphans).

CREATE TABLE plugin_version_log (
    -- UUID v7 PK — same convention as the marketplace plugin_versions
    -- table, which lets the version log sort time-ascending by id.
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- The plugin slug this row tracks. Not a foreign key (see file
    -- comment) but indexed for the dominant access pattern: "show me
    -- every recorded version for plugin X".
    slug            TEXT NOT NULL
                    CHECK (slug ~ '^[a-z][a-z0-9-]{2,40}$'),

    -- The version string at the time of install. Stored as text;
    -- semver comparison is done at the application layer using the
    -- same library the catalog uses.
    version         TEXT NOT NULL
                    CHECK (length(version) > 0 AND length(version) <= 64),

    -- ABI version the bundle declared. Tracked here so a rollback
    -- can re-establish the right ABI guards without re-reading the
    -- bundle.
    abi_version     INT NOT NULL CHECK (abi_version > 0),

    -- One of: 'active', 'retiring', 'retained', 'retired'.
    -- Constrained at the DB layer so a buggy caller can't poison the
    -- log; the lifecycle.VersionState constants are the source of
    -- truth for what each value means.
    state           TEXT NOT NULL
                    CHECK (state IN ('active', 'retiring', 'retained', 'retired')),

    installed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- When the row most recently transitioned to 'active'. Set on
    -- AppendActive and on PromoteToActive; null for rows that were
    -- never active (none today, but the column lets a future "stage
    -- but don't activate" gesture record itself here).
    activated_at    TIMESTAMPTZ,

    -- When the row transitioned out of 'active' into 'retiring'.
    -- Null while the row is current.
    retired_at      TIMESTAMPTZ,

    -- When the row becomes eligible for purge. Null on active rows.
    -- The cleanup cron deletes rows whose retention_end < now.
    retention_end   TIMESTAMPTZ,

    -- A given (slug, version) pair appears at most once in the log.
    -- A re-install of the same version is a no-op; rollback toggles
    -- state on the existing row.
    UNIQUE (slug, version)
);

COMMENT ON TABLE plugin_version_log IS
    'Per-host version log used by the lifecycle Manager for atomic update / rollback / retention (issue #63).';
COMMENT ON COLUMN plugin_version_log.state IS
    'active = current; retiring = draining post-swap; retained = warm for rollback; retired = unloaded, awaiting cron purge.';
COMMENT ON COLUMN plugin_version_log.retention_end IS
    'When a retained row becomes eligible for cron purge. Null on active / retiring / retired rows.';

-- Partial index for "find the active version for slug X" — single-row
-- per slug invariant lets this index degenerate to a unique constraint
-- on (slug) WHERE state='active'. Postgres treats partial unique
-- indexes as proper constraints, which is exactly what we want here.
CREATE UNIQUE INDEX plugin_version_log_active_idx
    ON plugin_version_log (slug)
    WHERE state = 'active';

-- "Show me every retained version for this slug, newest first" —
-- the dominant Rollback read pattern.
CREATE INDEX plugin_version_log_retained_idx
    ON plugin_version_log (slug, installed_at DESC)
    WHERE state = 'retained';

-- The cleanup cron walks rows ordered by retention_end so a single
-- index scan covers the entire purge pass.
CREATE INDEX plugin_version_log_retention_end_idx
    ON plugin_version_log (retention_end)
    WHERE retention_end IS NOT NULL;
