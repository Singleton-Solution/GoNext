-- 000025_webhook_subscriptions.down.sql
--
-- Reverse of 000025_webhook_subscriptions.up.sql.
--
-- Order matters: webhook_deliveries depends on webhook_subscriptions
-- via FK. DROP TABLE IF EXISTS removes the table and its indexes in
-- one shot; we explicitly drop deliveries first so the rollback
-- doesn't rely on CASCADE (which would silently mask a stray FK we
-- didn't expect).

DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_subscriptions;
