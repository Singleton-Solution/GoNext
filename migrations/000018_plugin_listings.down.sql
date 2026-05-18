-- 000018_plugin_listings.down.sql
--
-- Reverse of 000018_plugin_listings.up.sql.
--
-- The trigger is dropped implicitly with the table. The trigger
-- function is shared with future marketplace tables (000019–000022),
-- so we drop it here only because this is the lowest-numbered table
-- that depends on it — when the migration tree is unwound in order,
-- 000022 down → 000021 down → … → 000018 down, the function has no
-- remaining users by the time this file runs.
--
-- IF EXISTS so a partial rollback after a failed intermediate state
-- still completes cleanly.

DROP TABLE IF EXISTS plugin_listings;
DROP FUNCTION IF EXISTS marketplace_touch_updated_at();
