-- 000025_webhook_subscriptions.up.sql
--
-- Webhook subscriptions + delivery audit log.
--
-- The wider webhook surface was scaffolded in #104 (events, signer) and
-- the delivery worker landed in #348 (retry classifier, dead-letter
-- audit, signature header). Both layers operate on a *Subscription*
-- abstraction whose Postgres backing was deliberately deferred to this
-- migration: until there's an admin UI capable of issuing CRUD calls,
-- a persisted subscription store is dead code.
--
-- This migration introduces two tables:
--
--   * webhook_subscriptions — long-lived configuration. One row per
--     subscriber endpoint. Owned by an operator (created_by FK to
--     users); carries the event filter, HMAC secret, status fields the
--     delivery worker writes back on every attempt, and a soft
--     "degraded" mark the deadletter pipeline can set when retries are
--     exhausted.
--
--   * webhook_deliveries — append-only audit log. One row per attempt
--     (subscription_id, event_id, attempt). The admin UI's "recent
--     deliveries" pane reads from here. Retention is by TTL (cleaned
--     up by a background job) — we never UPDATE these rows, so an old
--     entry is safe to drop wholesale.
--
-- PII posture:
--
--   * The HMAC secret is stored as a BYTEA in the subscriptions table.
--     This is the raw signing key — the Subscription record is the
--     authoritative store, and the delivery worker reads it via a
--     SecretResolver that fetches the bytes at delivery time. Rotation
--     is an UPDATE of secret + a new secret_version (a follow-up
--     migration introduces versioned secrets if the operator demand
--     materializes; for now the rotation flow is "regenerate and
--     re-subscribe").
--
--   * The deliveries table stores the response status, latency,
--     headers (truncated), and a payload preview — never the raw
--     request body. That's intentional: the admin UI must be able to
--     show what came back from the subscriber without us re-storing
--     the entire event we just sent (the event store owns that copy).
--
-- Depends on:
--   * 000001_init — gen_uuid_v7(), pgcrypto.
--   * 000002_users — created_by FK target.

-- =============================================================================
-- webhook_subscriptions
-- =============================================================================
--
-- The configuration row an operator manages via the admin UI. One row
-- per subscriber endpoint; the worker picks rows where active = true
-- and the event matches the events[] filter, then enqueues one
-- webhook:deliver task per match.

CREATE TABLE webhook_subscriptions (
    -- Time-sortable UUID v7. Joins against users (created_by) keep
    -- clustering predictable.
    id                              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Operator-facing label. The admin UI renders this in the list view
    -- and in audit log entries. Required and non-empty so admins can
    -- distinguish multiple subscriptions to the same URL ("Shopify mirror"
    -- vs "internal analytics"). 200 chars is plenty for a human label
    -- while bounding the worst case.
    name                            TEXT NOT NULL
                                    CHECK (length(name) > 0 AND length(name) <= 200),

    -- The absolute URL the worker POSTs to. https:// is mandatory in
    -- production; the delivery package validates this on each send so
    -- the constraint here is intentionally loose — we accept http:// at
    -- the DB level so dev/test fixtures (httptest servers) work without
    -- migration changes.
    url                             TEXT NOT NULL
                                    CHECK (length(url) > 0 AND length(url) <= 2048),

    -- Subscribed event names (dotted, e.g. "post.published",
    -- "comment.created", "webhook.test"). Empty array matches nothing,
    -- so the worker treats it as "subscription disabled" without
    -- needing a separate "no events" status; admins reach the same
    -- state by toggling active.
    events                          TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],

    -- HMAC signing key. Raw bytes; the delivery worker passes them to
    -- hmac.New(sha256.New, secret) verbatim. 32 bytes (256 bits) is
    -- the default the admin UI generates on create; longer keys are
    -- accepted but the column has a soft cap to defend against a
    -- pathological "1MB secret" insert.
    secret                          BYTEA NOT NULL
                                    CHECK (octet_length(secret) BETWEEN 16 AND 512),

    -- Operator switch. Workers skip rows where active=false; the
    -- subscription record stays so a re-enable is a one-click action
    -- rather than a "recreate from scratch".
    active                          BOOLEAN NOT NULL DEFAULT TRUE,

    -- Audit anchor. SET NULL on user delete so an admin leaving the
    -- platform doesn't tombstone their subscriptions; the row remains
    -- valid (the worker doesn't care who created it).
    created_by                      UUID
                                    REFERENCES users(id) ON DELETE SET NULL,

    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Status fields written by the delivery worker on every attempt.
    -- last_delivery_at + last_delivery_status are the inputs to the
    -- admin UI's "healthy / degraded" badge — operators don't have to
    -- crack open the deliveries log to see "did the last call succeed?".
    last_delivery_at                TIMESTAMPTZ,
    last_delivery_status            TEXT
                                    CHECK (last_delivery_status IS NULL OR last_delivery_status IN ('success', 'retry', 'failed')),
    last_delivery_response_code     INTEGER
                                    CHECK (last_delivery_response_code IS NULL OR (last_delivery_response_code BETWEEN 0 AND 599)),

    -- Consecutive failures counter. Resets to 0 on the next success.
    -- The deadletter pipeline writes degraded_at when this counter (or
    -- the retry schedule) is exhausted; the admin UI uses degraded_at
    -- to render the "degraded" badge and lift the row to the top of
    -- the list.
    consecutive_failures            INTEGER NOT NULL DEFAULT 0
                                    CHECK (consecutive_failures >= 0),

    -- When non-NULL, the subscription is in the "degraded" state.
    -- The delivery worker keeps sending — the mark is informational
    -- for operators, not a kill switch. Clearing this is an explicit
    -- admin action (the "Reset" button in the UI).
    degraded_at                     TIMESTAMPTZ
);

-- The delivery worker's hot read is "find subscriptions where active
-- and events @> ARRAY[<event>]". Postgres can use a GIN index over
-- the events array to serve that membership test efficiently; without
-- it the worker degrades to a sequential scan on every event.
CREATE INDEX webhook_subscriptions_events_gin_idx
    ON webhook_subscriptions USING GIN (events);

-- The admin UI's list view sorts newest-first.
CREATE INDEX webhook_subscriptions_created_at_idx
    ON webhook_subscriptions (created_at DESC);

-- Partial index on degraded rows so the admin UI's "show me the
-- broken subscriptions" filter doesn't scan healthy rows.
CREATE INDEX webhook_subscriptions_degraded_idx
    ON webhook_subscriptions (degraded_at)
    WHERE degraded_at IS NOT NULL;

-- =============================================================================
-- webhook_deliveries
-- =============================================================================
--
-- Append-only audit log of every delivery attempt. The admin UI's
-- detail page lists the most recent N rows for a subscription so an
-- operator can answer "why are my webhooks failing?" without grepping
-- worker logs.
--
-- We deliberately do NOT store the request body — the event store
-- owns the canonical payload and we don't want a second copy with its
-- own retention semantics. We DO store the response code, the
-- response body preview (first ~1 KiB), the latency, and the error
-- classification, because those are the per-attempt data points the
-- worker uniquely owns.

CREATE TABLE webhook_deliveries (
    -- BIGSERIAL — volume is unbounded (every attempt of every event
    -- adds a row). We don't surface the row id externally; the
    -- natural key is (subscription_id, event_id, attempt) and the
    -- table is read by subscription, so a synthetic PK plus that
    -- compound index is enough.
    id                      BIGSERIAL PRIMARY KEY,

    subscription_id         UUID NOT NULL
                            REFERENCES webhook_subscriptions(id) ON DELETE CASCADE,

    -- The event identifier as set by the producer. Constant across
    -- retry attempts so the subscriber can dedupe; also the column
    -- we group on for "all attempts of this event".
    event_id                TEXT NOT NULL
                            CHECK (length(event_id) > 0 AND length(event_id) <= 200),

    -- The event type / name (e.g. "post.published"). Stored here too
    -- (denormalized) so the admin UI's deliveries list doesn't have
    -- to join against an event-store table that may or may not be in
    -- the same database.
    event_type              TEXT NOT NULL
                            CHECK (length(event_type) > 0 AND length(event_type) <= 200),

    -- 1-based attempt number. Matches the delivery worker's
    -- Attempt field. Together with (subscription_id, event_id) it
    -- forms the natural unique key.
    attempt                 INTEGER NOT NULL CHECK (attempt >= 1),

    -- Outcome classification. Mirrors the delivery package's
    -- StatusSuccess/StatusRetry/StatusDeadletter trichotomy. The
    -- "test" value is for the operator-triggered test endpoint —
    -- those don't count toward the retry budget.
    status                  TEXT NOT NULL
                            CHECK (status IN ('success', 'retry', 'failed', 'test')),

    -- The HTTP status code returned by the subscriber. 0 when the
    -- request never produced a response (DNS, timeout, TLS error).
    response_code           INTEGER NOT NULL DEFAULT 0
                            CHECK (response_code BETWEEN 0 AND 599),

    -- Wire latency in milliseconds. Stored as INTEGER (not interval)
    -- because the admin UI renders it as a number with a "ms"
    -- suffix; saving the conversion step.
    duration_ms             INTEGER NOT NULL DEFAULT 0
                            CHECK (duration_ms >= 0),

    -- Response body preview, capped at ~1 KiB. NULL when no body
    -- was returned. The admin UI shows this in the detail drawer so
    -- an operator can read the subscriber's "your secret is wrong"
    -- error without re-hitting the endpoint.
    response_body_preview   TEXT
                            CHECK (response_body_preview IS NULL OR length(response_body_preview) <= 2048),

    -- Worker-side error message. Set when the attempt failed before
    -- producing a response (DNS, timeout) — distinct from
    -- response_code = 5xx, where the subscriber answered.
    error                   TEXT
                            CHECK (error IS NULL OR length(error) <= 1024),

    -- When the attempt was made. Defaults to row insertion time
    -- (worker writes immediately after the request closes).
    delivered_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Retention horizon. Defaults to 30 days; the cleanup job
    -- (a future addition) deletes rows past expires_at in batches.
    expires_at              TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '30 days')
);

-- Primary read path: "give me the last N deliveries for this
-- subscription". (subscription_id, delivered_at DESC) is the natural
-- index for that query, and the partial index condition is implicit
-- in the FK.
CREATE INDEX webhook_deliveries_subscription_delivered_idx
    ON webhook_deliveries (subscription_id, delivered_at DESC);

-- Secondary read path: "show me every attempt for this event". Used
-- by the detail drawer in the admin UI when an operator expands an
-- event row to see the retry timeline.
CREATE INDEX webhook_deliveries_subscription_event_idx
    ON webhook_deliveries (subscription_id, event_id, attempt);

-- Retention sweep — same shape as rum_events.
CREATE INDEX webhook_deliveries_expires_at_idx
    ON webhook_deliveries (expires_at);
