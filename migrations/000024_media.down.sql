-- 000024_media.down.sql
--
-- Reverse of 000024_media.up.sql. DROP TABLE removes the indexes the
-- table owns; no separate DROP INDEX pass is needed. CASCADE is omitted
-- intentionally — a hidden FK pointing at media would be a bug we want
-- the down to surface rather than silently destroy.

DROP TABLE IF EXISTS media;
