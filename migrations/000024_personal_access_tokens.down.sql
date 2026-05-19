-- 000024_personal_access_tokens.down.sql
--
-- Rolls back the PAT table and its indexes. DROP TABLE drops indexes
-- with it, so the index DROPs are redundant but explicit for symmetry.

DROP INDEX IF EXISTS personal_access_tokens_expires_active_idx;
DROP INDEX IF EXISTS personal_access_tokens_user_active_idx;
DROP TABLE IF EXISTS personal_access_tokens;
