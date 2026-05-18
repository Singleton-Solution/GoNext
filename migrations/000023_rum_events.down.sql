-- 000018_rum_events.down.sql
--
-- Reverse of 000018_rum_events.up.sql. DROP TABLE removes the indexes
-- the table owns; no separate DROP INDEX pass is needed.
--
-- IF EXISTS so a partial rollback (after a failed intermediate state)
-- still completes cleanly. CASCADE is deliberately omitted — no other
-- migration in this sequence depends on rum_events, and a hidden FK
-- pointing here would be a bug we want the down to surface.

DROP TABLE IF EXISTS rum_events;
