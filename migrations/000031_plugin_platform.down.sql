-- 000031_plugin_platform.down.sql
--
-- Drop the tables created in 000031_plugin_platform.up.sql.
--
-- This is a destructive migration — running it on a production
-- database erases every plugin secret (which the plugin author may
-- have spent operator time provisioning) and every plugin cron
-- schedule. Per migrations/README.md, the production rollback path
-- is a new forward migration; this down file exists strictly for
-- the development reversibility loop.

DROP TABLE IF EXISTS plugin_cron_schedules;
DROP TABLE IF EXISTS plugin_secrets;
