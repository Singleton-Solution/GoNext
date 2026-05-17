-- 000004_posts.down.sql
--
-- Reverse of 000004_posts.up.sql. Order matters: indexes and triggers
-- belong to the table, so dropping the table would take them with it,
-- but we drop them explicitly for clarity and to keep the down
-- migration symmetric with the up migration.
--
-- IF EXISTS guards everywhere so partial rollbacks (e.g. after a
-- failed intermediate state) still complete cleanly.
--
-- Note on the trigger helpers (`touch_updated_at`, `bump_version`):
-- they are *not* dropped here. They were defined in this migration
-- because `posts` is the first table to need them, but they are
-- shared infrastructure that later tables (users — already there
-- — comments, terms, options, …) attach to as well. A DROP FUNCTION
-- here would either fail (because dependent triggers still exist on
-- those other tables) or break those tables if we used CASCADE. The
-- functions are cheap to leave in place; if a true full rollback to
-- the 000001 state is ever needed, that's a job for the relevant
-- down migration in whichever later table actually owns them, or for
-- an explicit DROP FUNCTION at the end of the chain.

-- =============================================================================
-- Indexes
-- =============================================================================

DROP INDEX IF EXISTS posts_meta_gin;
DROP INDEX IF EXISTS posts_parent_idx;
DROP INDEX IF EXISTS posts_author_idx;
DROP INDEX IF EXISTS posts_type_status_published_idx;
DROP INDEX IF EXISTS posts_slug_hier_uq;
DROP INDEX IF EXISTS posts_slug_flat_uq;

-- =============================================================================
-- Triggers
-- =============================================================================

DROP TRIGGER IF EXISTS posts_bump_version ON posts;
DROP TRIGGER IF EXISTS posts_touch_updated_at ON posts;

-- =============================================================================
-- Table
-- =============================================================================

DROP TABLE IF EXISTS posts;
