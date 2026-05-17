-- 000009_sessions.down.sql
--
-- Reverse of 000009_sessions.up.sql. The table owns its indexes, so
-- `DROP TABLE` removes them in the same statement; we don't need a
-- separate `DROP INDEX` pass.
--
-- `IF EXISTS` guard so a partial rollback (after a failed intermediate
-- state) still completes cleanly. The CASCADE option is deliberately
-- omitted: no other migration in this sequence depends on `sessions`,
-- so a hidden FK pointing here is a bug we want the down to surface.

DROP TABLE IF EXISTS sessions;
