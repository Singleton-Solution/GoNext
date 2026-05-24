-- 000028_options_installation_key_compat.up.sql
--
-- Aligns the "installation complete" marker key used by the CLI
-- (`gonext init`) and the in-browser setup handler. An early
-- version of the CLI wrote the marker under
-- "core.installation_completed_at"; the setup handler reads
-- "core.site.installation_completed_at" (matching the rest of the
-- `core.site.*` namespace). Databases bootstrapped by the pre-fix
-- CLI therefore appeared "not installed" to /api/v1/setup/status,
-- letting the install wizard re-arm itself on an already-bootstrapped
-- deployment.
--
-- This migration is a one-shot rename:
--
--   1. Copy the legacy row into the canonical key, preserving the
--      original RFC3339 timestamp value. ON CONFLICT DO NOTHING so
--      a deployment where both keys are somehow present (e.g. a
--      partial roll-forward by the in-process migration in
--      cli/gonext/cmd/init/setup.go) doesn't clobber a fresher
--      canonical value with the older legacy one.
--   2. Drop the legacy row. After this migration the codebase no
--      longer reads the legacy key from disk; the only remaining
--      reference is a defensive Has() fallback in
--      apps/api/internal/setup/handler.go for any database that
--      bootstrapped after migrate stamped this row but before the
--      defensive read landed (an empty window in practice).
--
-- Idempotent: a fresh database has no rows under either key, so
-- both statements are no-ops. Re-applying the migration is safe.

INSERT INTO options (key, value, autoload, is_protected)
SELECT 'core.site.installation_completed_at', value, TRUE, FALSE
FROM options
WHERE key = 'core.installation_completed_at'
ON CONFLICT (key) DO NOTHING;

DELETE FROM options WHERE key = 'core.installation_completed_at';
