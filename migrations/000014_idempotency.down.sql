-- 000014_idempotency.down.sql
--
-- Reverse of 000014_idempotency.up.sql. The table owns its indexes
-- so DROP TABLE removes them in the same statement; no separate
-- DROP INDEX pass is needed.
--
-- IF EXISTS so a partial rollback (after a failed intermediate state)
-- still completes cleanly. CASCADE is deliberately omitted — no other
-- migration in this sequence depends on idempotency_keys, so a hidden
-- FK pointing here would be a bug we want the down to surface.

DROP TABLE IF EXISTS idempotency_keys;
