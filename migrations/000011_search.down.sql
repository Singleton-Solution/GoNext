-- 000011_search.down.sql
--
-- Reverse of 000011_search.up.sql. Drops the FTS apparatus added on
-- `posts` in dependency order: trigger first (depends on function),
-- function next (no longer referenced), then the indexed column.
-- Dropping `search_vector` drops the GIN index `posts_search_vector_gin`
-- automatically, so it doesn't need its own DROP INDEX.
--
-- IF EXISTS guards everywhere so partial rollback (after an aborted
-- intermediate state) still completes cleanly.

-- Trigger first — must come down before the function it references.
DROP TRIGGER IF EXISTS posts_search_vector_trg ON posts;

-- Function next — nothing references it once the trigger is gone.
DROP FUNCTION IF EXISTS posts_search_vector_update();

-- Column last. The GIN index posts_search_vector_gin is dropped
-- implicitly by the column drop (Postgres always drops indexes
-- alongside the column they index).
ALTER TABLE posts
    DROP COLUMN IF EXISTS search_vector;
