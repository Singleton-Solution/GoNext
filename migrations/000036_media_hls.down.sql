-- 000036_media_hls.down.sql
--
-- Reverse of 000036_media_hls.up.sql. Drops the HLS playlist column
-- from the media table. The bytes on disk (m3u8 + segments) are NOT
-- swept by this migration — that's a separate purge cron concern;
-- this only releases the row-side reference.

ALTER TABLE media DROP COLUMN IF EXISTS hls_url;
