-- 000028_options_installation_key_compat.down.sql
--
-- Reverses 000028_options_installation_key_compat.up.sql: restores
-- the legacy "core.installation_completed_at" row by copying the
-- canonical row back, then drops the canonical row. Same ON CONFLICT
-- DO NOTHING / DELETE ordering as the up migration so a deployment
-- that already has both keys (because the in-process compat path in
-- the CLI ran independently) doesn't end up with neither.
--
-- The down migration exists only to make rollback symmetric. A
-- production deployment that has run the setup handler since the
-- fix landed should NOT roll this back — the handler is the
-- authoritative writer of the canonical key, and dropping it would
-- re-open the install wizard.

INSERT INTO options (key, value, autoload, is_protected)
SELECT 'core.installation_completed_at', value, TRUE, FALSE
FROM options
WHERE key = 'core.site.installation_completed_at'
ON CONFLICT (key) DO NOTHING;

DELETE FROM options WHERE key = 'core.site.installation_completed_at';
