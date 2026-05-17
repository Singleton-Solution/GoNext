-- 000010_post_locks.down.sql
--
-- Reverse of 000010_post_locks.up.sql. Drops are ordered so that
-- dependents come down before their dependencies:
--
--   1. The acquire_post_lock() function (depends on the table).
--   2. The post_locks table (drops its indexes implicitly).
--
-- IF EXISTS guards everywhere so partial rollbacks (after a failed
-- intermediate state) still complete cleanly. See
-- migrations/README.md — `down` is for local-dev iteration, not
-- production rollback; the latter is handled by a forward fix
-- migration per the expand/contract rule in docs/09-deployment-ops.md.

-- Function first (depends on the table type).
DROP FUNCTION IF EXISTS acquire_post_lock(UUID, UUID, UUID, INTERVAL);

-- Table last (drops post_locks_user_acquired_idx with it).
DROP TABLE IF EXISTS post_locks;
