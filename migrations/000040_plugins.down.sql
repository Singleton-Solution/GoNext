-- 000040_plugins.down.sql
--
-- Drops the plugins lifecycle table, its state index, and the
-- updated_at trigger. The shared `touch_updated_at()` function stays
-- in place — other tables (posts, comments, …) depend on it.

DROP TRIGGER IF EXISTS plugins_touch_updated_at ON plugins;
DROP INDEX IF EXISTS plugins_state_idx;
DROP TABLE IF EXISTS plugins;
