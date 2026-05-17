-- 000012_post_revisions.down.sql
--
-- Reverses 000012_post_revisions.up.sql. Drops the indexes and the
-- post_revisions table.
--
-- The `revision_kind` enum is owned by 000001_init and stays in place
-- — other migrations and a future re-up of this one rely on it.
--
-- See migrations/README.md: down migrations exist for development
-- iteration. Production schema rollbacks must be done via a new
-- forward-only migration per the expand/contract rule.

DROP INDEX IF EXISTS post_revisions_pruner_idx;
DROP INDEX IF EXISTS post_revisions_kind_idx;
DROP INDEX IF EXISTS post_revisions_post_created_idx;

DROP TABLE IF EXISTS post_revisions;
