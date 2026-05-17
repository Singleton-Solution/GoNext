-- 000003_post_types.down.sql
--
-- Reverse of 000003_post_types.up.sql. Drops are ordered dependents
-- first so partial rollback states are still recoverable:
--
--   1. Trigger (depends on the table and the function).
--   2. Function (named with a post_types_ prefix so it is owned by
--      this migration and not a sibling).
--   3. Table.
--
-- IF EXISTS on every drop so that a half-applied up migration can
-- still be rolled back cleanly.
--
-- Note (also in 000001_init.down.sql): running `down` in production
-- is not the supported way to undo a change — write a new forward
-- migration instead. See migrations/README.md.

-- Trigger first (cheap, depends on the function and table).
DROP TRIGGER IF EXISTS post_types_bump_updated_at ON post_types;

-- Function next. Safe to drop now that no trigger references it.
DROP FUNCTION IF EXISTS post_types_bump_updated_at();

-- Table last. The CASCADE is deliberately omitted: at this point in
-- the schema history nothing should reference post_types yet (posts
-- arrives in #55). If a future migration adds an FK to post_types and
-- forgets to drop it in its own down, the explicit failure here is
-- the signal to fix that migration rather than silently destroying
-- referencing rows.
DROP TABLE IF EXISTS post_types;
