-- 000033_gdpr_anonymization.down.sql
--
-- Reverse of the GDPR anonymization columns. We drop the index first
-- because it depends on the column; Postgres would refuse to drop the
-- column otherwise without a CASCADE we'd rather make explicit here.
--
-- Note: rolling back this migration does NOT un-anonymize users whose
-- data was already zeroed by the delete handler. Anonymization is a
-- one-way operation and the destroyed PII can never be recovered. The
-- columns are merely the scheduling metadata.

DROP INDEX IF EXISTS users_scheduled_purge_idx;

ALTER TABLE users
    DROP COLUMN IF EXISTS scheduled_purge_at,
    DROP COLUMN IF EXISTS anonymized_at;
