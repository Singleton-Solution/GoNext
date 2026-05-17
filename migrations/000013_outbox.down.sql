-- 000013_outbox.down.sql
--
-- Reverse migration 000013. Drops the outbox table and the two
-- supporting partial indexes (which fall away with the table). Down
-- migrations exist for development; production rollback is by
-- forward-only migration per the rules in migrations/README.md.

DROP TABLE IF EXISTS outbox;
