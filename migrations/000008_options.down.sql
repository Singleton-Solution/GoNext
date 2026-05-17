-- 000008_options.down.sql
--
-- Reverse of 000008_options.up.sql. Order matches the standard
-- "dependents before dependencies" convention used everywhere else in
-- this migration set:
--
--   1. Trigger (depends on the table + function).
--   2. Function (depends on nothing once the trigger is gone).
--   3. Table (drops the partial index implicitly).
--
-- IF EXISTS guards on every drop so partial rollbacks complete cleanly.
-- See migrations/README.md for the forward-only rule in production —
-- this file exists for the dev round-trip (`up → down → up`).

DROP TRIGGER IF EXISTS options_touch ON options;
DROP FUNCTION IF EXISTS options_touch();

-- Dropping the table also drops options_autoload_idx, the seed rows,
-- and the column COMMENTs.
DROP TABLE IF EXISTS options;
