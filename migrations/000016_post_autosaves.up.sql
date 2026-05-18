-- 000016_post_autosaves.up.sql
--
-- Editor autosave staging table. While a user is actively editing a
-- post via the block editor, the client posts a snapshot of the
-- in-flight block tree every ~30s to /api/v1/posts/{id}/autosave.
-- The snapshot is stashed here (keyed by post + author) so a tab
-- crash or accidental reload doesn't lose work — on next mount, the
-- editor checks GET /api/v1/posts/{id}/autosave, compares against
-- the canonical posts row, and offers "restore unsaved draft" if
-- the autosave is newer.
--
-- Why not bump posts.content_blocks directly? Two reasons. First,
-- autosave is a tentative state — it must not generate revisions,
-- must not fire publish webhooks, must not invalidate caches. A
-- separate table keeps every other system blissfully unaware that
-- autosave even happened. Second, a single (post_id, user_id) key
-- means concurrent collaborators (post_lock contention aside)
-- maintain independent unsaved drafts: Alice's autosave doesn't
-- clobber Bob's. The post_lock guard in the handler still rejects
-- concurrent writes, but the schema doesn't depend on it.
--
-- Spec: docs/04-block-editor.md §6 ("Autosave + recovery") and the
-- design discussion in issue #146. Depends on:
--   * posts(id)       — migration 000004
--   * users(id)       — migration 000002
--   * post_locks      — migration 000010 (locked by handler, not FK)
-- Both must be present in main before this migration is applied.
--
-- TTL: the GC sweep in packages/go/jobs/autosave_sweep.go drops rows
-- whose updated_at is older than 7 days. We don't enforce this at
-- the schema level (a partition / pg_cron job is overkill for the
-- expected volume); the sweep runs hourly in production and is
-- idempotent on each pass.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE post_autosaves (
    -- The canonical post this autosave belongs to. CASCADE so that
    -- when a post is hard-deleted (out of the scope of soft-trash),
    -- its dangling autosaves go with it — they'd be unreachable
    -- anyway since the editor can't open a non-existent post.
    post_id     UUID NOT NULL
                REFERENCES posts(id) ON DELETE CASCADE,

    -- The user whose unsaved draft this is. CASCADE: a deleted
    -- user's autosaves are tombstones — nothing else will ever
    -- claim them, and they'd block a clean user delete. The
    -- post_lock acquire path is by user_id too, so the natural
    -- pairing is preserved on both sides.
    user_id     UUID NOT NULL
                REFERENCES users(id) ON DELETE CASCADE,

    -- The autosaved block tree. Stored as JSONB so the editor can
    -- pull selective subtrees on recovery without parsing the
    -- whole thing (future optimization — for now we read the
    -- whole column). Matches the shape of posts.content_blocks
    -- exactly; the handler does a copy-through with no rewriting.
    blocks      JSONB NOT NULL,

    -- Last time this autosave was overwritten. Updated on every
    -- ON CONFLICT DO UPDATE pass; the editor compares against
    -- posts.updated_at to decide whether the autosave is newer
    -- than the canonical version (= "we have unsaved work").
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Compound PK enforces "one autosave per (post, user)" at the
    -- schema layer — every autosave call upserts in place rather
    -- than appending. This is what makes "latest wins" the only
    -- coherent semantic; we never have to pick between two rows.
    PRIMARY KEY (post_id, user_id)
);

-- =============================================================================
-- Indexes
-- =============================================================================

-- Sweep index: "find every autosave older than $now - 7d so we can
-- TRUNCATE them". Without this, the sweep job's WHERE updated_at <
-- $threshold is a sequential scan over the whole table. btree on
-- updated_at lets the planner use a range scan.
CREATE INDEX post_autosaves_updated_at_idx
    ON post_autosaves (updated_at);

-- "What is this user's currently-autosaved work?" — used by the
-- admin "you have unsaved drafts" indicator. btree on (user_id,
-- updated_at DESC) so the most-recent-first listing needs no sort.
CREATE INDEX post_autosaves_user_updated_idx
    ON post_autosaves (user_id, updated_at DESC);
