-- 000034_magic_link_tokens.down.sql
--
-- Rolls back the magic link token table and its indexes. DROP TABLE
-- drops indexes with it, so the index DROPs are redundant but explicit
-- for symmetry with the up migration.

DROP INDEX IF EXISTS magic_link_tokens_expires_idx;
DROP INDEX IF EXISTS magic_link_tokens_user_active_idx;
DROP TABLE IF EXISTS magic_link_tokens;
