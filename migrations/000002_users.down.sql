-- 000002_users.down.sql
--
-- Reverse of 000002_users.up.sql. Drops are ordered so that dependents
-- come down before their dependencies:
--
--   1. user_passwords (depends on users via FK).
--   2. users trigger, then users table.
--   3. The shared touch_updated_at_and_version() function — safe to drop
--      because no other migration has landed yet that uses it. If a
--      future migration adopts the trigger, this `down` will need to
--      stop dropping the function (the rule in migrations/README.md:
--      forward-only after merge).
--
-- IF EXISTS guards everywhere so partial rollbacks (after a failed
-- intermediate state) still complete cleanly.

-- Dependents first.
DROP TABLE IF EXISTS user_passwords;

-- Trigger before its function (a function cannot be dropped while a
-- trigger references it).
DROP TRIGGER IF EXISTS users_touch_updated_at ON users;

DROP TABLE IF EXISTS users;

-- Shared trigger function.
DROP FUNCTION IF EXISTS touch_updated_at_and_version();
