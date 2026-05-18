-- 000016_task_redactions.down.sql
--
-- Reverse of 000016_task_redactions.up.sql. DROP TABLE removes the
-- indexes the table owns; no separate DROP INDEX pass is needed.
--
-- IF EXISTS so a partial rollback (after a failed intermediate state)
-- still completes cleanly. CASCADE is deliberately omitted — no other
-- migration in this sequence depends on task_redactions, so a hidden
-- FK pointing here would be a bug we want the down to surface.

DROP TABLE IF EXISTS task_redactions;
