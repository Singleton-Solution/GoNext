-- 000015_migration_map.down.sql
--
-- Reverse of 000015_migration_map.up.sql. The table owns its index
-- so DROP TABLE removes it in the same statement; no separate
-- DROP INDEX pass is needed.
--
-- IF EXISTS so a partial rollback (after a failed intermediate
-- state) still completes cleanly. CASCADE is deliberately omitted
-- — no other migration in this sequence depends on migration_map,
-- so a hidden FK pointing here would be a bug we want the down to
-- surface.

DROP TABLE IF EXISTS migration_map;
