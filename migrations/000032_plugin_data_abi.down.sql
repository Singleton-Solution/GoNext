-- 000032_plugin_data_abi.down.sql
--
-- Reverse 000032_plugin_data_abi.up.sql. Order matters: the FK on
-- plugin_kv_index references plugin_kv_quotas, so the index table
-- drops first.

DROP INDEX IF EXISTS cache_invalidations_unconsumed_idx;
DROP TABLE IF EXISTS cache_invalidations;

DROP INDEX IF EXISTS plugin_kv_index_evict_idx;
DROP TABLE IF EXISTS plugin_kv_index;
DROP TABLE IF EXISTS plugin_kv_quotas;

DROP TABLE IF EXISTS plugin_db_roles;
