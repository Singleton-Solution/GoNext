-- 000016_post_autosaves.down.sql
--
-- Reverse of 000016_post_autosaves.up.sql. Drops are ordered so that
-- the indexes come down implicitly with the table; no separate
-- DROP INDEX needed.
--
-- IF EXISTS guards so partial rollbacks (after a failed intermediate
-- state) still complete cleanly. See migrations/README.md — `down`
-- is for local-dev iteration, not production rollback; the latter
-- is handled by a forward fix migration per the expand/contract
-- rule in docs/09-deployment-ops.md.

DROP TABLE IF EXISTS post_autosaves;
