-- 000020_plugin_compat_matrix.down.sql
--
-- Reverse of 000020_plugin_compat_matrix.up.sql. DROP TABLE takes the
-- index with it.

DROP TABLE IF EXISTS plugin_compat_matrix;
