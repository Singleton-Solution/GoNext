-- 000035_media_collections.down.sql
--
-- Reverts 000035_media_collections.up.sql. Drops the FK column from
-- media first (so the collections table can be dropped without
-- breaking the reference), then the table itself. We do NOT drop
-- the ltree / citext extensions — other future tables may want
-- them, and DROP EXTENSION is destructive across the whole
-- database.

DROP INDEX IF EXISTS media_tags_gin_idx;
ALTER TABLE media DROP COLUMN IF EXISTS tags;

DROP INDEX IF EXISTS media_collection_id_idx;
ALTER TABLE media DROP COLUMN IF EXISTS collection_id;

DROP INDEX IF EXISTS media_collections_parent_idx;
DROP INDEX IF EXISTS media_collections_path_btree_idx;
DROP INDEX IF EXISTS media_collections_path_gist_idx;
DROP INDEX IF EXISTS media_collections_root_slug_idx;
DROP TABLE IF EXISTS media_collections;
