-- 000033_password_reset_tokens.down.sql
--
-- Rolls back the password reset token table and its indexes. DROP TABLE
-- drops indexes with it, so the index DROPs are redundant but explicit
-- for symmetry with the up migration.

DROP INDEX IF EXISTS password_reset_tokens_expires_idx;
DROP INDEX IF EXISTS password_reset_tokens_user_active_idx;
DROP TABLE IF EXISTS password_reset_tokens;
