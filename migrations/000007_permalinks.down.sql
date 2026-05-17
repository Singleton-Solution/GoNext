-- 000007_permalinks.down.sql
--
-- Reverse of 000007_permalinks.up.sql. Drop order is reverse of
-- creation: `redirects` first (it has no dependents and references
-- nothing of ours), then `permalinks`. Both tables have indexes and
-- comments that the DROP TABLE cascade removes automatically.
--
-- IF EXISTS guards everywhere so a partial-rollback (after a failed
-- intermediate state) still completes cleanly. Same convention as
-- 000001_init.down.sql.
--
-- Note: as documented in migrations/README.md, production rollback is
-- handled via a new forward migration, not by running `down`. This
-- file exists so that a developer can `migrate up && migrate down 1
-- && migrate up` cleanly during PR review.

DROP TABLE IF EXISTS redirects;
DROP TABLE IF EXISTS permalinks;
