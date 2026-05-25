-- 000029_audit_log.down.sql
--
-- Drop the audit_log table created in 000029_audit_log.up.sql.
--
-- This is a destructive migration — running it on a production
-- database erases every audit event written since the install
-- bootstrapped, which is the opposite of what an audit trail is for.
-- Per migrations/README.md "no destructive operations without an
-- ADR", the production rollback path is to write a new forward
-- migration that captures whatever schema change the operator
-- actually wants; this down file exists strictly for the
-- `migrate up && migrate down 1 && migrate up` reversibility loop
-- used during development.
--
-- The indexes are dropped implicitly with the table.

DROP TABLE IF EXISTS audit_log;
