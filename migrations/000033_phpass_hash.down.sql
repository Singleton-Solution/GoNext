-- 000033_phpass_hash.down.sql — revert 000033_phpass_hash.up.sql.

DROP INDEX IF EXISTS users_legacy_phpass_hash_idx;
ALTER TABLE users DROP COLUMN IF EXISTS legacy_phpass_hash;
