-- 000033_gdpr_anonymization.up.sql
--
-- GDPR "right to erasure" support (issue #216).
--
-- Two columns on `users` drive the two-phase deletion flow:
--
--   1. anonymized_at      — set the moment the user calls POST
--                            /api/v1/account/data/delete. The handler
--                            soft-deletes the row (status='deleted'),
--                            zeroes PII columns in place, and stamps
--                            this timestamp. The user is gone from
--                            the application's perspective immediately.
--
--   2. scheduled_purge_at — set to anonymized_at + 30d. The
--                            gdpr.purge.tick cron task (apps/worker)
--                            hard-deletes any row whose
--                            scheduled_purge_at <= now(). The 30-day
--                            grace window covers GDPR Art. 17 §3 (we
--                            must keep enough breadcrumbs to honor a
--                            recovery request from law-enforcement or
--                            a successful "I clicked by accident"
--                            ticket) while staying inside the 30-day
--                            ceiling our DPA promises.
--
-- Both columns are nullable: an "active" or "suspended" user has
-- NULL for both. The partial index on scheduled_purge_at lets the
-- cron task scan only the live deletion queue without a sequential
-- scan of the users table.
--
-- We deliberately do NOT add a CHECK that scheduled_purge_at >=
-- anonymized_at: a future re-run of the purge job may want to clear
-- only anonymized_at (after the hard-delete row migration moves
-- forensic data to the audit log).
--
-- Related code:
--   * apps/api/internal/account/data — REST handlers
--   * apps/worker/internal/tasks/gdpr — purge.tick cron task

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS anonymized_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS scheduled_purge_at TIMESTAMPTZ;

COMMENT ON COLUMN users.anonymized_at IS
    'Stamped when the user invokes GDPR delete. PII columns are zeroed in the same UPDATE; status becomes ''deleted''.';
COMMENT ON COLUMN users.scheduled_purge_at IS
    'When the hard-delete cron should remove the row. Set to anonymized_at + 30d by the delete handler; NULL for live users.';

-- Partial index: the cron task SELECTs WHERE scheduled_purge_at <= now()
-- AND scheduled_purge_at IS NOT NULL. The partial predicate keeps the
-- index pages small (only deleted users), and the BTREE on the timestamp
-- gives the cron's range scan O(log N + k) behavior.
CREATE INDEX IF NOT EXISTS users_scheduled_purge_idx
    ON users (scheduled_purge_at)
    WHERE scheduled_purge_at IS NOT NULL;
