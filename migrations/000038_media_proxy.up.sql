-- 000038_media_proxy.up.sql
--
-- Adds proxy-mode columns to the media table — backs issue #187.
-- When a WP migration runs in proxy mode, the importer registers
-- each remote attachment as a media row without copying the bytes;
-- requests to the asset's URL pass through the existing image
-- proxy (#37), which fetches from source_url on first hit and
-- caches the response.
--
-- Design notes
--
--   * is_proxied DEFAULTs FALSE so every existing media row (which
--     was uploaded directly) retains its current semantics. The
--     column flips to TRUE only for rows the migrator inserts in
--     proxy mode.
--
--   * source_url is NULL by default and only populated for proxied
--     rows. The CHECK keeps the two columns coupled: a proxied row
--     MUST have a source URL, and a non-proxied row MUST NOT (the
--     latter half guards against an operator accidentally setting
--     source_url and forgetting to flip the flag).
--
--   * The storage_key column on a proxied row is left as a synthetic
--     placeholder (e.g. "proxy/<source-url-hash>") so the UNIQUE
--     constraint still applies and the rest of the codebase can
--     treat the row uniformly. The proxy handler routes off
--     is_proxied, not the key shape.
--
-- Depends on:
--   * 000024_media — parent table.

ALTER TABLE media
    ADD COLUMN is_proxied BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN source_url TEXT
        CHECK (source_url IS NULL OR length(source_url) <= 2048);

-- The two columns are coupled: proxied rows MUST carry a source_url,
-- and non-proxied rows MUST NOT. Keeping the invariant in the schema
-- so a buggy importer can't half-write a row.
ALTER TABLE media
    ADD CONSTRAINT media_proxy_url_consistent
    CHECK (
        (is_proxied = TRUE  AND source_url IS NOT NULL) OR
        (is_proxied = FALSE AND source_url IS NULL)
    );

-- Index proxied rows for the migration audit view ("which assets
-- are we proxying and how many?"). Partial-on so the index stays
-- tiny on a non-migrated deployment.
CREATE INDEX media_is_proxied_idx
    ON media (created_at DESC)
    WHERE is_proxied = TRUE;

COMMENT ON COLUMN media.is_proxied IS
    'TRUE when the media row references a remote source served via the image proxy (issue #187). FALSE for normally uploaded assets.';
COMMENT ON COLUMN media.source_url IS
    'Origin URL for proxied media rows. Read-through cached by the proxy on first hit. NULL for non-proxied rows.';
