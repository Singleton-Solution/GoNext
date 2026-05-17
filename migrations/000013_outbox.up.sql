-- 000013_outbox.up.sql
--
-- Transactional outbox table. See issue #260 and the design discussion
-- in docs/12-jobs-cron.md (forthcoming §"Transactional outbox").
--
-- Problem this solves: today a handler that wants to enqueue a Redis
-- job after a DB write does
--
--     tx.Commit()
--     redis.Enqueue(...)
--
-- and a crash between the two leaves the system inconsistent — the DB
-- change happened but the worker never fires. The "transactional
-- outbox" pattern fixes this by treating the queue write as an
-- ordinary database write: the handler inserts into `outbox` *inside*
-- its own transaction, and a separate poller process drains the table
-- and forwards entries to Redis. Delivery is at-least-once (the poller
-- may crash between enqueue + delete), and the DB row remains the
-- single source of truth.
--
-- Schema sized for the steady-state load: thousands of rows in flight,
-- not millions. If volume ever justifies it the table can be
-- partitioned by created_at without changing the application contract.
--
-- Reference: Hohpe + Woolf (EIP) "Guaranteed Delivery"; Microservices
-- transactional-outbox pattern (microservices.io/patterns).

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE outbox (
    -- Monotonic ID. We use BIGSERIAL (not UUID v7) here because the
    -- poller orders strictly by primary-key for FIFO scheduling and
    -- BIGSERIAL is a cheap single-column btree. Outbox rows never
    -- escape the system — they're deleted on successful enqueue —
    -- so the lack of timestamp-prefix isn't a B-tree-bloat concern.
    id          BIGSERIAL PRIMARY KEY,

    -- The task / handler name the worker dispatches on. Mirrors the
    -- string the handler would have passed to redis.Enqueue.
    task_name   TEXT NOT NULL,

    -- Opaque payload forwarded to the worker. JSONB rather than BYTEA
    -- so we can inspect entries via psql on a production incident
    -- without writing a decoder.
    payload     JSONB NOT NULL,

    -- Destination queue / stream name. NULL is rejected so an empty
    -- enqueue never silently falls into the default queue.
    queue       TEXT NOT NULL,

    -- When the handler wrote the row. Used for FIFO ordering and for
    -- "stuck for N minutes" alerts.
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- claimed_at + claimed_by form the lease. NULL means "ready for
    -- the next poll cycle". Non-NULL means a poller has taken it and
    -- will either delete it on success or release the claim on
    -- failure. The recovery sweep relies on these columns to detect
    -- stuck workers (claimed_at older than ClaimLeaseSec).
    claimed_at  TIMESTAMPTZ,
    claimed_by  TEXT,

    -- Failed-enqueue bookkeeping. attempts increments every time we
    -- fail to forward the row; last_error captures the most recent
    -- failure message for diagnostics. Neither field gates retry —
    -- the poller treats every unclaimed row equally.
    attempts    INT NOT NULL DEFAULT 0,
    last_error  TEXT
);

-- =============================================================================
-- Indexes
-- =============================================================================
--
-- The poll-hot-path query is
--
--   SELECT id FROM outbox
--    WHERE claimed_at IS NULL
--    ORDER BY created_at
--    LIMIT :n
--    FOR UPDATE SKIP LOCKED
--
-- We want a partial index that holds only the unclaimed rows: claimed
-- rows in flight are the minority, and a full index would force the
-- planner to traverse delete-tombstones during routine polls. The
-- partial index automatically prunes rows the moment the poller
-- updates claimed_at — no maintenance burden, no autovacuum tuning.
CREATE INDEX outbox_unclaimed_idx
    ON outbox (created_at)
    WHERE claimed_at IS NULL;

-- Recovery sweep: "find rows whose lease expired". Indexed by
-- claimed_at so the periodic UPDATE that releases stuck rows
-- doesn't degrade into a seq scan. Partial on `claimed_at IS NOT
-- NULL` so the index stays small (only in-flight rows are present).
CREATE INDEX outbox_claimed_idx
    ON outbox (claimed_at)
    WHERE claimed_at IS NOT NULL;
