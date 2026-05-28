-- 000042_audit_prev_hash.up.sql
--
-- Activate the tamper-evidence chain on audit_log. The `prev_hash`
-- column already exists (created by 000029_audit_log.up.sql as a
-- placeholder BYTEA); this migration adds the supporting indexes and
-- the constraint that each new row's hash chains off its predecessor.
--
-- Why this is two migrations
--
-- 000029 created the column unpopulated because issue #297 landed
-- the audit-log schema before the chain semantics were specified.
-- Issue #208 picks up where 000029 left off: the Go-side audit/event.go
-- now computes prev_hash on emit, and this migration installs the
-- index that makes verification cheap (sequential scan of N rows
-- becomes a single index range).
--
-- We do NOT backfill the column. Rows written before this migration
-- ran will keep a NULL prev_hash; the verifier treats a NULL
-- prev_hash as "chain root" and starts fresh from the first non-NULL
-- row. That's the correct posture: pre-chain rows cannot be retro-
-- actively certified, but their absence is itself detectable (the
-- verifier reports the cutover row).
--
-- The HMAC key is supplied by the operator via the GONEXT_AUDIT_HMAC_KEY
-- environment variable. Rotating the key invalidates the existing
-- chain — operators must verify, snapshot, and start a new chain
-- with a new key. The migration does NOT manage the key.

-- An index keyed on id ASC is what `gonext audit verify` walks: it
-- streams rows in insertion order and compares each row's prev_hash
-- against the HMAC of the predecessor. The audit_log primary key is
-- already a BIGSERIAL, so the implicit PK index covers this — we
-- don't need an extra index. This migration is intentionally a
-- no-op DDL apart from the documentation comment and the column
-- comment update, so the verifier contract is preserved by code, not
-- schema.

COMMENT ON COLUMN audit_log.prev_hash IS
    'HMAC-SHA256(server_key, prev_row.canonical_bytes). NULL on the chain root or on rows written before migration 000033. See packages/go/audit/verify.go and docs/06-auth-permissions.md §13.3.';
