-- 000020_plugin_compat_matrix.up.sql
--
-- Marketplace data model — compatibility matrix.
--
-- A plugin version declares which host ABI ranges it has been tested
-- against. The marketplace UI surfaces this so a user running host
-- ABI 3 can see at a glance whether a plugin "Works", "Should work
-- (untested)", or "Not compatible".
--
-- A version may declare multiple ranges — e.g. tested against 1.x and
-- 3.x but not 2.x (perhaps because of a known regression that was
-- fixed in 3.0). Hence the composite PK on (plugin_version_id,
-- host_min, host_max) rather than a single row per version.
--
-- Depends on:
--   * 000019_plugin_versions — for the plugin_version_id FK target.

CREATE TABLE plugin_compat_matrix (
    -- The version this row describes. ON DELETE CASCADE because compat
    -- claims have no meaning outside the context of their version.
    plugin_version_id   UUID NOT NULL
                        REFERENCES plugin_versions(id) ON DELETE CASCADE,

    -- Minimum host ABI version this range applies to (inclusive).
    -- TEXT rather than INT because the host versioning scheme is
    -- semver-shaped and we want operators to be able to declare
    -- "1.0.0" vs "1.4.2" granularity in the future.
    host_min            TEXT NOT NULL
                        CHECK (length(host_min) > 0 AND length(host_min) <= 64),

    -- Maximum host ABI version (inclusive). Empty string is rejected;
    -- "any" callers should use a high sentinel like "999.0.0".
    host_max            TEXT NOT NULL
                        CHECK (length(host_max) > 0 AND length(host_max) <= 64),

    -- Whether the publisher actually ran the plugin against this
    -- range. FALSE means "the range is declared compatible but we
    -- haven't exercised it under CI". The marketplace UI shows this
    -- as a distinct badge.
    tested              BOOLEAN NOT NULL DEFAULT FALSE,

    -- Compound PK: a version may declare multiple ranges, but each
    -- (min, max) tuple appears at most once per version. This lets
    -- callers UPSERT on the same (min, max) when they re-publish the
    -- matrix.
    PRIMARY KEY (plugin_version_id, host_min, host_max),

    -- Sanity: host_min <= host_max under lexicographic comparison.
    -- Lex isn't perfect for semver ("10" sorts before "9") — the Go
    -- store performs the real semver comparison before insert. This
    -- CHECK catches the obvious operator-typo class only.
    CHECK (host_min <= host_max)
);

COMMENT ON TABLE  plugin_compat_matrix IS
    'Per-version host ABI compatibility claims. Composite key allows multiple disjoint ranges per version.';
COMMENT ON COLUMN plugin_compat_matrix.tested IS
    'TRUE = exercised under CI by the publisher. FALSE = declared compatible but unverified.';

-- Reverse lookup: "given a host running version X, which plugin
-- versions claim to support it?" This is the query the marketplace
-- filter UI runs when the user toggles "compatible with my host".
-- (host_min, host_max) is the natural index for the range check.
CREATE INDEX plugin_compat_matrix_range_idx
    ON plugin_compat_matrix (host_min, host_max);
