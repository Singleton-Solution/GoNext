-- 000019_plugin_versions.down.sql
--
-- Reverse of 000019_plugin_versions.up.sql. The indexes are owned by
-- the table; DROP TABLE removes them. No CASCADE — if a later
-- migration adds an unexpected dependency we want the down to surface
-- the bug rather than silently take the dependents with it.

DROP TABLE IF EXISTS plugin_versions;
