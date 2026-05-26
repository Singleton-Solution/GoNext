-- 000033_phpass_hash.up.sql
--
-- Add `users.legacy_phpass_hash` — a read-only carry column for
-- WordPress migrations. The WP password format (`$P$...` / `$H$...`)
-- is an MD5-stretched hash with a cost factor of typically 2^11 (the
-- "B" cost char). It is strictly weaker than the argon2id format
-- GoNext stores natively in `user_passwords.password_hash`, so we
-- never WRITE phpass hashes — we only verify them on the first
-- post-migration login and immediately re-hash with argon2id.
--
-- Lifecycle of a migrated user:
--
--   1. The WP importer (packages/go/migrate/importer) inserts the
--      user with their phpass hash here and a placeholder argon2id
--      hash in user_passwords.password_hash (the "always invalid"
--      sentinel — see importer.Options.PlaceholderPasswordHash).
--   2. First login: the auth path calls
--      packages.go/auth/password.VerifyPhpass; on success it
--      computes Hash(password) (argon2id), updates
--      user_passwords.password_hash, and clears
--      users.legacy_phpass_hash in the SAME transaction.
--   3. Every subsequent login goes through the normal argon2id
--      verifier; the legacy column is NULL and ignored.
--
-- We store the hash as plain TEXT (not bytea, not citext) because
-- phpass encoded strings are ASCII by construction (the alphabet is
-- "./0-9A-Za-z") and the column is compared exactly — no case
-- folding wanted.
--
-- See:
--   * issue #197 — phpass migration plan
--   * packages/go/auth/password/phpass.go — the read-only verifier
--   * docs/08-migration-compat.md §18 — migrated-user lifecycle

ALTER TABLE users
    ADD COLUMN legacy_phpass_hash TEXT NULL;

-- A partial index supports the "is this user still on the legacy
-- hash?" admin query (we want to surface counts of un-migrated
-- accounts in the migration report). The vast majority of rows have
-- legacy_phpass_hash IS NULL post-rollover, so a partial index keeps
-- it compact.
CREATE INDEX users_legacy_phpass_hash_idx
    ON users ((1))
    WHERE legacy_phpass_hash IS NOT NULL;

COMMENT ON COLUMN users.legacy_phpass_hash IS
    'WordPress phpass hash carried over from a migration. Read-only: the auth path verifies, then re-hashes with argon2id and clears this column. NULL after first successful login.';
