-- 000038_media_proxy.down.sql
--
-- Reverse of 000038_media_proxy.up.sql. DROP COLUMN cascades to the
-- CHECK constraint and the partial index automatically; the data
-- migration is destructive (proxied rows lose their origin URL),
-- which is acceptable since this is the development-reversibility
-- path — production rollbacks go through a forward migration.

ALTER TABLE media
    DROP CONSTRAINT IF EXISTS media_proxy_url_consistent,
    DROP COLUMN IF EXISTS source_url,
    DROP COLUMN IF EXISTS is_proxied;
