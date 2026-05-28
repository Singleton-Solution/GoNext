-- 000042_audit_prev_hash.down.sql
--
-- Reverse of 000042_audit_prev_hash.up.sql. Since the up only
-- updated the column comment (the prev_hash column itself was added
-- by 000029), the down restores the original placeholder comment.

COMMENT ON COLUMN audit_log.prev_hash IS NULL;
