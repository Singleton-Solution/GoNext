-- 000012_post_revisions.up.sql
--
-- Block-editor revision history. Every autosave, manual save, and
-- publish event appends one row. Storage is snapshot-or-delta:
--
--   * snapshot revisions carry the full editable JSON in `snapshot`
--   * delta revisions carry an RFC 6902 JSON Patch in `delta`, with
--     `delta_from` pointing at the parent revision the patch applies
--     against.
--
-- The CHECK constraint enforces (snapshot IS NOT NULL) XOR
-- (delta IS NOT NULL). The matching Go contract lives in
-- packages/go/revisions/ and was locked by PR #326.
--
-- Spec: docs/01-core-cms.md §4 (revisions semantics) and §10.6 (DDL).
-- Depends on:
--   * 000001_init       — gen_uuid_v7(), revision_kind enum
--   * 000002_users      — users(id) for author_id FK
--   * 000004_posts      — posts(id) for post_id FK
--
-- # is_permanent
--
-- Marks a revision as never-prunable by the retention sweep
-- (packages/go/revisions/pruner.go). The retention job respects this
-- flag unconditionally — operators flip it on revisions that carry
-- legal-hold, "first published" milestones, or anything else the
-- editor lets a user pin. Default FALSE so existing rows are eligible
-- for normal retention. See issue #169.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE post_revisions (
    -- Primary key. UUID v7 so inserts cluster at the tail of the index
    -- and rows naturally sort chronologically (ADR 0003).
    id                   UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Owning post. CASCADE: deleting a post removes its history.
    -- The editor never offers a way to delete a post without nuking
    -- its revisions, so this is the contract the app already relies on.
    post_id              UUID NOT NULL
                         REFERENCES posts(id) ON DELETE CASCADE,

    -- Who wrote the revision. SET NULL on user delete: the audit
    -- trail of who-edited-what survives a user being removed, but
    -- the FK detaches so the row doesn't pin the users row.
    author_id            UUID REFERENCES users(id) ON DELETE SET NULL,

    -- When the revision was created. Stable — never rewritten.
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- autosave / manual / publish. The enum is owned by 000001 so it
    -- can be shared with other tables that classify edit events.
    kind                 revision_kind NOT NULL,

    -- Snapshot path: full editable JSON at the moment of save.
    -- NULL on delta rows; the CHECK constraint below enforces the
    -- XOR with `delta`.
    snapshot             JSONB,

    -- Delta path: parent pointer + RFC 6902 patch.
    --
    -- delta_from is the revision this patch applies against. It MUST
    -- reference another row in this same table when set. We do NOT
    -- declare ON DELETE for this FK: the retention pruner is required
    -- to never delete a snapshot that's still reachable from an
    -- un-pruned delta (see packages/go/revisions/pruner.go), so the
    -- FK should never be orphaned. If it ever is, an integrity error
    -- at write time is the right loud failure mode.
    delta_from           UUID REFERENCES post_revisions(id),
    delta                JSONB,

    -- Denormalized post fields, captured at save time so the
    -- revisions-list UI doesn't need a JOIN against posts for the
    -- row label. Cheap to store, saves a JSONB walk per row.
    title                TEXT,
    excerpt              TEXT,

    -- Hash of `content_blocks` at save time. The post layer computes
    -- this with whatever algorithm it uses for the pre-render cache;
    -- revisions just stores the bytes. Useful for "no material change
    -- since the last save" checks without re-parsing the JSON.
    content_blocks_hash  BYTEA,

    -- Operator-supplied annotation. The restore flow writes
    -- "Restored from revision X" here (doc 01 §4.4); an editor can
    -- attach a free-form note ("renamed section") on a manual save.
    comment              TEXT,

    -- Pinned-forever flag. The retention pruner (issue #169) skips
    -- any row with is_permanent=TRUE no matter how old or how far
    -- past the count cap it is. Operators flip this on legal-hold
    -- revisions, the first-published milestone, etc. Default FALSE
    -- so the bulk of revisions are still eligible for normal sweep.
    is_permanent         BOOLEAN NOT NULL DEFAULT FALSE,

    -- Exactly-one-payload invariant. Both stores enforce this at the
    -- Go layer too, but the constraint here is the load-bearing one:
    -- it makes the table physically incapable of holding a row that
    -- isn't classifiable as snapshot-or-delta.
    CONSTRAINT post_revisions_snapshot_xor_delta_chk
        CHECK ((snapshot IS NOT NULL) <> (delta IS NOT NULL))
);

-- =============================================================================
-- Indexes
-- =============================================================================

-- The revisions-list UI queries `WHERE post_id = $1 ORDER BY
-- created_at DESC` on every editor open. Index supports both the
-- filter and the sort, so the query is a single backwards index walk.
CREATE INDEX post_revisions_post_created_idx
    ON post_revisions (post_id, created_at DESC);

-- The "latest of kind" lookup (e.g. "what was the last publish?")
-- and the per-kind retention count are both
-- (post_id, kind, created_at DESC). Separate from the
-- post_created_idx so the planner doesn't have to scan over autosaves
-- when the editor asks for the most recent publish.
CREATE INDEX post_revisions_kind_idx
    ON post_revisions (post_id, kind, created_at DESC);

-- The retention pruner (issue #169) scans across all posts ordered by
-- created_at to find prunable candidates and uses FOR UPDATE SKIP
-- LOCKED so multiple pruner instances can run in parallel without
-- double-deleting. This partial index covers the not-permanent
-- subset that pruning actually touches.
CREATE INDEX post_revisions_pruner_idx
    ON post_revisions (post_id, created_at)
    WHERE is_permanent = FALSE;
