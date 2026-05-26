-- 000039_plugin_version_log.down.sql

DROP INDEX IF EXISTS plugin_version_log_retention_end_idx;
DROP INDEX IF EXISTS plugin_version_log_retained_idx;
DROP INDEX IF EXISTS plugin_version_log_active_idx;
DROP TABLE IF EXISTS plugin_version_log;
