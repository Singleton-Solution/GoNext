-- 000035_custom_fields.down.sql

DROP INDEX IF EXISTS idx_post_meta_values_group;
DROP INDEX IF EXISTS idx_field_groups_post_types;
DROP TABLE IF EXISTS post_meta_values;
DROP TABLE IF EXISTS field_groups;
