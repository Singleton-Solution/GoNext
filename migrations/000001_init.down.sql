-- 000001_init.down.sql
--
-- Reverse of 000001_init.up.sql. Drops are ordered so that dependents
-- come down before their dependencies:
--
--   1. Enums (no tables exist yet that reference them, but ordering
--      this way matches the convention later migrations will follow).
--   2. gen_uuid_v7() function.
--   3. Extensions.
--
-- IF EXISTS guards everywhere so partial rollbacks (after a failed
-- intermediate state) still complete cleanly.
--
-- Note: dropping extensions can disrupt other databases that happen to
-- share the cluster but not the schema; in practice we run one
-- database per cluster in dev/test and never invoke `down` in
-- production. See migrations/README.md and docs/09-deployment-ops.md §13.

-- Enums first (reverse order of creation).
DROP TYPE IF EXISTS revision_kind;
DROP TYPE IF EXISTS post_status;

-- Function next.
DROP FUNCTION IF EXISTS gen_uuid_v7();

-- Extensions last (reverse order of creation).
DROP EXTENSION IF EXISTS ltree;
DROP EXTENSION IF EXISTS citext;
DROP EXTENSION IF EXISTS pgcrypto;
