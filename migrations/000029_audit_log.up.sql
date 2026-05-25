-- 000029_audit_log.up.sql
--
-- Durable audit-event table backing packages/go/audit/PostgresStore.
--
-- The Go-side contract was locked by issue #297 in
-- packages/go/audit/postgres.go: the INSERT statement names a specific
-- column list (`occurred_at`, `actor_user_id`, `actor_kind`,
-- `actor_label`, `event`, `target_kind`, `target_id`, `ip`,
-- `user_agent`, `metadata`, `severity`, `prev_hash`), the cast on
-- `actor_user_id` is BIGINT (see docs/06-auth-permissions.md §13), and
-- the SELECT path orders by `occurred_at DESC, id DESC`. This
-- migration is the schema half of that contract — the Go store was
-- shipped first specifically so the DDL could land in a separate,
-- reviewable PR without blocking the package compile.
--
-- Until this migration runs, `audit.NewEmitter(audit.NewPostgresStore(pool))`
-- compiles but any Emit call fails with `pgx UndefinedTable`. The
-- comment on `audit/postgres.go:PostgresStore` documents that
-- expectation; main.go now wires PostgresStore unconditionally
-- (replacing the MemoryStore plug used during #297 → #54 transit).
--
-- ──────────────────────────────────────────────────────────────────
-- Why BIGSERIAL, not UUID
-- ──────────────────────────────────────────────────────────────────
--
-- ADR 0003 mandates UUID v7 for primary keys EXCEPT where a
-- domain-specific story trumps it. The audit log is one of those:
--
--   * Audit rows are append-only and ORDERED. A monotonic 64-bit PK
--     makes "give me the next N events after id X" a single B-tree
--     range scan; UUID v7 would work too, but every SIEM exporter on
--     the planet expects a numeric cursor (Splunk HEC, Datadog
--     forwarder, the upcoming SOC2 export path). BIGSERIAL keeps the
--     export contract trivial.
--
--   * The Go contract already returns id::TEXT via INSERT...RETURNING
--     so the column type is opaque to callers.
--
--   * Partitioning by month (planned in a follow-up; see
--     docs/06-auth-permissions.md §13.2) is straightforward with a
--     numeric PK + timestamp range partitions.
--
-- ──────────────────────────────────────────────────────────────────
-- Why no FK on actor_user_id
-- ──────────────────────────────────────────────────────────────────
--
-- doc 06 §13 declares `actor_user_id BIGINT REFERENCES users(id)`.
-- In this codebase users.id is UUID (see 000002_users.up.sql) — a
-- carry-over from the ADR 0003 rollout that the audit-log doc hasn't
-- caught up to yet. Reconciling the two is a separate follow-up
-- (docs/_audit/audit_actor_type.md is the placeholder); for v1 we
-- ship the audit table WITHOUT the FK and treat actor_user_id as a
-- soft reference. Rows survive user deletion (which is the right
-- audit posture anyway — "user 42 was deleted" is itself an audit
-- event, and the events that user emitted before deletion remain
-- their forensic record).
--
-- The Go store passes ActorUserID as a string and casts via
-- `NULLIF($2, '')::BIGINT` in postgres.go. If a caller ever passes a
-- non-integer string the cast fails at INSERT time with a clear pgx
-- error; that's the correct posture during the UUID-vs-BIGINT
-- transition — we crash loudly rather than silently storing garbage.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE audit_log (
    -- BIGSERIAL gives us a monotonic export cursor (see header
    -- comment). The Go contract returns this as id::TEXT through
    -- INSERT...RETURNING so the column type is invisible to handlers.
    id              BIGSERIAL PRIMARY KEY,

    -- Wall-clock time the event occurred. Defaults to NOW() but the
    -- Go store always sends an explicit value (Event.normalize),
    -- which lets back-fill imports preserve the original timestamp.
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Nullable: pre-auth events (failed login) and system-internal
    -- actions have no acting user. See header re: no FK.
    actor_user_id   BIGINT,

    -- One of 'user', 'plugin', 'system'. Enforced as a CHECK rather
    -- than an ENUM so the set can grow without a TYPE alter — and so
    -- the matching Go-side validator (isValidActorKind in postgres.go)
    -- can be the single source of truth. Adding a fourth value
    -- requires a migration AND a Go change; that's the right level of
    -- friction for an auditable enum.
    actor_kind      TEXT NOT NULL
                    CHECK (actor_kind IN ('user', 'plugin', 'system')),

    -- Free-form label for the actor — typically a plugin slug when
    -- actor_kind = 'plugin', empty otherwise. NULLIF coalesces empty
    -- strings to NULL on the Go side so this column is meaningfully
    -- "present or absent".
    actor_label     TEXT,

    -- Dotted event name: 'auth.login.success', 'plugin.activated',
    -- '{plugin_slug}.{noun}.{verb}'. Length-capped to keep a buggy
    -- emitter from blowing out the row.
    event           TEXT NOT NULL
                    CHECK (length(event) > 0 AND length(event) <= 128),

    -- What the event targeted. Free-form ('post', 'user', 'setting',
    -- 'plugin', etc). Empty for non-targeted events like a failed
    -- login.
    target_kind     TEXT,
    target_id       TEXT,

    -- Source IP as INET. The Go store casts a string through
    -- NULLIF($8, '')::INET; pgx propagates parse errors so a
    -- malformed IP fails the INSERT loudly rather than silently
    -- storing a bad row.
    ip              INET,

    -- Raw User-Agent header, truncated to 1024 bytes on the Go side
    -- (audit.event.go:userAgentMax). No CHECK here — truncation is
    -- enforced upstream and a strict CHECK would just turn a benign
    -- oversized UA into a hard error mid-emit.
    user_agent      TEXT,

    -- Per-event extra context. JSONB so the SIEM export can extract
    -- structured fields (metadata->>'request_id', etc.) without a
    -- second parse pass. Default '{}' matches the Go side's "if
    -- nil, send {}" rule.
    metadata        JSONB NOT NULL DEFAULT '{}'::JSONB,

    -- Severity classifier. CHECK pinned to the documented enum;
    -- adding a value requires this migration plus the matching Go
    -- Severity.Valid() in event.go.
    severity        TEXT NOT NULL DEFAULT 'info'
                    CHECK (severity IN ('info', 'warning', 'critical')),

    -- Reserved for the future HMAC tamper-evidence chain (see
    -- docs/06-auth-permissions.md §13.3 and audit/event.go:PrevHash).
    -- v1 always stores NULL; landing the column now avoids a schema
    -- bump when the chain implementation arrives.
    prev_hash       BYTEA
);

-- =============================================================================
-- Indexes
-- =============================================================================
--
-- Three access patterns drive the read paths through this table:
--
--   1. "show me everything user X did, newest first" — admin UI per-
--      user audit page. Matched by audit_actor_idx.
--   2. "show me all events of type T, newest first" — operator
--      filtering by event_type ('auth.login.failed' is the canonical
--      example). Matched by audit_event_idx.
--   3. "show me everything that targeted resource R" — investigation
--      flow ("who touched post 42?"). Matched by audit_target_idx.
--
-- The retention sweep does a single bounded DELETE keyed on
-- occurred_at; the per-event indexes carry the occurred_at column
-- in their second slot so the sweep can be planned as an index-only
-- scan without a dedicated occurred_at index.

CREATE INDEX audit_actor_idx
    ON audit_log (actor_user_id, occurred_at DESC)
    WHERE actor_user_id IS NOT NULL;

CREATE INDEX audit_event_idx
    ON audit_log (event, occurred_at DESC);

CREATE INDEX audit_target_idx
    ON audit_log (target_kind, target_id, occurred_at DESC)
    WHERE target_kind IS NOT NULL;

-- Sweep index — supports the retention-prune query
-- `DELETE FROM audit_log WHERE occurred_at < $1 AND severity = 'info'`
-- (critical/warning rows are retained per docs/06-auth-permissions.md
-- §13.2). Partial index keeps it tiny — most events are 'info' but
-- the rare 'critical' row would otherwise inflate the index for no
-- benefit because the sweep never deletes those.
CREATE INDEX audit_occurred_sweep_idx
    ON audit_log (occurred_at)
    WHERE severity = 'info';

COMMENT ON TABLE audit_log IS
    'Append-only audit trail. Schema locked by issue #297 (packages/go/audit). See docs/06-auth-permissions.md §13.';
