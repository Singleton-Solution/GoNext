-- 000016_task_redactions.up.sql
--
-- DLQ task redactions. When a background task fails and is archived in
-- Asynq's "archived" set, admins inspect the payload to understand why
-- it failed. Payloads sometimes carry sensitive material — API tokens
-- echoed back from a third-party request, customer email addresses
-- bundled into a webhook envelope, etc. The DLQ admin UI (issue #262)
-- lets an operator mark specific JSON paths inside an archived task's
-- payload as "redact this on subsequent reads", and the listing handler
-- substitutes `***REDACTED***` for those fields before rendering.
--
-- Why a small Postgres table and not a Redis key?
--
--   - The Asynq archived set lives in Redis, but redactions are an
--     audit-style record: who masked which fields, when. Postgres is
--     the right tier for that — durable across Redis flushes, queryable
--     for the "who redacted X" question, joinable against the audit log.
--   - Asynq itself owns the archived task lifecycle (it eventually
--     prunes archived tasks after configurable retention). When a
--     task is replayed or discarded the redaction record can be left
--     in place; an FK would mean Postgres has to know about Asynq's
--     keyspace, which we don't want.
--   - The redaction record is read-only after creation. An admin who
--     needs to *un*-redact a field re-issues the redact action with
--     a smaller field set; old records are kept for the audit trail.
--
-- Depends on:
--   * 000001_init — for the pgcrypto extension (not directly used here
--     but the table is part of the same schema's extension chain).
--   * 000002_users — for the redacted_by FK target. We don't enforce it
--     as an ON DELETE CASCADE — when a user is deleted we want the
--     audit trail to persist with the user_id NULL'd by hand or via a
--     scheduled tombstoner; cascading would erase the operator trail.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE task_redactions (
    -- The Asynq task ID (a UUID-shaped string returned by Inspector
    -- when it lists archived tasks). One row per task — if the same
    -- task is redacted again, the row is UPSERTed (the application
    -- layer merges the field set, see actions.go:applyRedaction).
    --
    -- TEXT because Asynq's task ID is an opaque string from our
    -- perspective; we don't parse it. Length-capped defensively.
    task_id          TEXT PRIMARY KEY
                     CHECK (length(task_id) > 0 AND length(task_id) <= 255),

    -- The queue this task lived on at the time of redaction. Stored so
    -- the listing handler can look up the redaction without needing to
    -- consult Asynq first ("show me all redactions on queue X" is a
    -- useful operator query). Not in the PK because (queue, task_id)
    -- redundantly identifies the same task — Asynq IDs are globally
    -- unique across queues.
    queue            TEXT NOT NULL
                     CHECK (length(queue) > 0 AND length(queue) <= 255),

    -- The JSON paths (top-level field names in the payload object) to
    -- redact. Stored as a TEXT[] rather than JSONB because the typical
    -- access pattern is "is field X in the set?" and TEXT[] gives us a
    -- cheap GIN-indexable contains operator without JSONB's overhead.
    --
    -- The application layer constrains entries to top-level field
    -- names (no nested paths in v1) and rejects empty arrays at the
    -- handler boundary — a redaction with zero fields is a discard,
    -- not a redaction.
    redacted_fields  TEXT[] NOT NULL
                     CHECK (array_length(redacted_fields, 1) >= 1),

    -- When the redaction was first applied. Read-only column.
    redacted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The user who applied the redaction. ON DELETE SET NULL so the
    -- audit row survives the user record. The FK is named explicitly
    -- so dropping it in a follow-up migration is unambiguous.
    redacted_by      UUID
                     REFERENCES users(id) ON DELETE SET NULL
);

-- =============================================================================
-- Indexes
-- =============================================================================

-- "List all redactions on queue X" — supports the admin diagnostics
-- view that filters by queue. Without this it's a sequential scan over
-- the table.
CREATE INDEX task_redactions_queue_idx
    ON task_redactions (queue);

-- "Who did this user redact, when?" — feeds the per-operator audit
-- panel. Compound (redacted_by, redacted_at) so the natural sort by
-- recency is index-served.
CREATE INDEX task_redactions_redacted_by_at_idx
    ON task_redactions (redacted_by, redacted_at DESC);
