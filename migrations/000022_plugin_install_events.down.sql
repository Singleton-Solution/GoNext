-- 000022_plugin_install_events.down.sql
--
-- Reverse of 000022_plugin_install_events.up.sql. DROP TABLE drops
-- the indexes and the BIGSERIAL's underlying sequence.

DROP TABLE IF EXISTS plugin_install_events;
