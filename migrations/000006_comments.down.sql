-- 000006_comments.down.sql
--
-- Reverse of 000006_comments.up.sql. Drops are ordered so that
-- dependents come down before their dependencies:
--
--   1. Indexes on `comments` (so the planner can't reference them
--      mid-drop; technically Postgres handles this on DROP TABLE
--      but we list them explicitly to mirror the up file).
--   2. Triggers on `comments`.
--   3. The table itself.
--   4. The trigger functions specific to comments. The shared
--      helpers (touch_updated_at, bump_version) belong to other
--      migrations and are NOT dropped here — other tables still
--      depend on them.
--
-- IF EXISTS guards everywhere so partial rollbacks complete cleanly.

-- Triggers first (the table drop would cascade, but explicit is kinder
-- to anyone reading a partial-rollback log).
DROP TRIGGER IF EXISTS comments_version ON comments;
DROP TRIGGER IF EXISTS comments_touch ON comments;
DROP TRIGGER IF EXISTS comments_reparent_descendants ON comments;
DROP TRIGGER IF EXISTS comments_set_path_upd ON comments;
DROP TRIGGER IF EXISTS comments_set_path_ins ON comments;

-- Indexes (also implicitly dropped by DROP TABLE, but listed for
-- symmetry with the up file).
DROP INDEX IF EXISTS comments_pending_idx;
DROP INDEX IF EXISTS comments_author_user_idx;
DROP INDEX IF EXISTS comments_path_idx;
DROP INDEX IF EXISTS comments_post_status_created_idx;

-- Table.
DROP TABLE IF EXISTS comments;

-- Trigger functions specific to this migration. touch_updated_at()
-- and bump_version() are shared and belong to whichever migration
-- introduced them — do NOT drop them here.
DROP FUNCTION IF EXISTS comments_reparent_descendants();
DROP FUNCTION IF EXISTS comments_set_path();
