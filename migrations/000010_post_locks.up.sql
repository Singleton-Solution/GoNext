-- 000010_post_locks.up.sql
--
-- Editor concurrent-editing protection. When Alice opens the editor
-- for a post, the client calls acquire_post_lock() and the row in
-- post_locks marks the post as "Alice is editing this". If Bob then
-- opens the same post, the editor renders the "Alice is editing —
-- view-only, or steal-lock?" banner instead of letting him save over
-- her work. The editor heartbeats every ~30s to push expires_at
-- forward; if the heartbeat stops (tab closed, browser crashed) the
-- lock falls out of date and the next caller takes it.
--
-- Spec: docs/01-core-cms.md §10.13 ("Editor locks") and the design
-- discussion in issue #106. Depends on:
--   * posts(id)  — migration 000004
--   * users(id)  — migration 000002
-- Both must be present in main before this migration is applied.
--
-- We diverge from the doc's two-column DDL in two small ways, both
-- spec'd in the issue:
--   1. We carry heartbeat_at separately from expires_at so the GC
--      job can distinguish "lock expired because the user idled" from
--      "lock expired because the editor crashed". expires_at is what
--      acquire_post_lock() checks; heartbeat_at is observational.
--   2. We carry session_id (nullable) so server-side flows that
--      don't have a sessions row (e.g. an admin script, an n8n-style
--      automation) can still hold a lock. The FK is deliberately
--      ON DELETE SET NULL: revoking a session shouldn't free the
--      lock — only expiry should.

-- =============================================================================
-- Table
-- =============================================================================

CREATE TABLE post_locks (
    -- One lock per post. PK on post_id (rather than a synthetic id)
    -- enforces that at the schema level — no need to defend against
    -- two concurrent inserts winning the race.
    post_id      UUID PRIMARY KEY
                 REFERENCES posts(id) ON DELETE CASCADE,

    -- Who holds the lock.
    user_id      UUID NOT NULL
                 REFERENCES users(id) ON DELETE CASCADE,

    -- When the lock was first taken. Stable across heartbeats so the
    -- UI can show "Alice has been editing for 7 minutes".
    acquired_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- When the lock auto-releases. The editor pushes this forward
    -- on every heartbeat; acquire_post_lock() compares against now().
    expires_at   TIMESTAMPTZ NOT NULL,

    -- Last keep-alive ping. Equal to acquired_at on first insert,
    -- bumped to now() on every heartbeat. Lets the GC job report
    -- "last activity was N seconds before expiry" for debugging
    -- without needing to subtract from expires_at.
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Optional pointer to the sessions row this lock was acquired
    -- under. Nullable so non-session callers (scripts, automation)
    -- still work. SET NULL on delete: a revoked session shouldn't
    -- free an otherwise-still-valid lock — only the expiry check in
    -- acquire_post_lock() should be able to do that.
    session_id   UUID
                 REFERENCES sessions(id) ON DELETE SET NULL
);

-- "What is this user currently editing right now?" — used by the
-- admin nav to badge the "you have an unsaved draft" indicator and
-- by the GC job to bulk-release a user's locks on logout. btree on
-- (user_id, acquired_at) so the per-user list comes back ordered
-- newest-first without a sort step.
CREATE INDEX post_locks_user_acquired_idx
    ON post_locks (user_id, acquired_at);

-- =============================================================================
-- acquire_post_lock(post_id, user_id, session_id, ttl) — returns the
-- user_id of whoever currently holds an unexpired lock.
-- =============================================================================
--
-- Contract (from issue #106):
--   * If the post is unlocked (no row) OR the existing lock has
--     expired, upsert the requester in and return NULL — "you got it".
--   * If a *different* user holds an unexpired lock, return their
--     user_id — "they have it, render the steal-lock banner".
--   * If the same user re-calls (e.g. they opened a second tab),
--     refresh expires_at + heartbeat_at and return NULL.
--
-- The function is the *only* path the editor uses to take a lock.
-- Doing it in a function (rather than from Go) means the whole
-- read-then-write happens inside one statement against one row,
-- under Postgres' row-level locking — no application-side race.
--
-- We use INSERT ... ON CONFLICT DO UPDATE rather than DELETE + INSERT
-- so that:
--   * The post_id row is row-locked by the INSERT path the entire
--     time, preventing a second caller from sneaking in between.
--   * acquired_at survives a same-user re-acquire (we only refresh
--     it when somebody *new* takes the lock).
--
-- Returns NULL when the caller now holds the lock. Returns the
-- existing holder's user_id when the caller did not get it.

CREATE OR REPLACE FUNCTION acquire_post_lock(
    p_post_id    UUID,
    p_user_id    UUID,
    p_session_id UUID,
    p_ttl        INTERVAL
) RETURNS UUID AS $$
DECLARE
    v_current_holder UUID;
    v_current_expiry TIMESTAMPTZ;
BEGIN
    -- Snapshot the current state under FOR UPDATE so we serialise
    -- against any concurrent caller. If no row exists, this returns
    -- nothing and we fall through to the INSERT path.
    SELECT user_id, expires_at
      INTO v_current_holder, v_current_expiry
      FROM post_locks
     WHERE post_id = p_post_id
     FOR UPDATE;

    -- Someone else holds an unexpired lock. Return their user_id;
    -- the caller (the editor) will render the steal-lock banner.
    IF v_current_holder IS NOT NULL
       AND v_current_holder <> p_user_id
       AND v_current_expiry >= now()
    THEN
        RETURN v_current_holder;
    END IF;

    -- Either nobody held it, it expired, or we already hold it.
    -- Upsert: if no row, insert; otherwise refresh expires_at and
    -- heartbeat_at. acquired_at is only touched on a *new* take
    -- (i.e. the holder changed); same-user re-acquires preserve it.
    INSERT INTO post_locks (
        post_id, user_id, acquired_at, expires_at, heartbeat_at, session_id
    ) VALUES (
        p_post_id,
        p_user_id,
        now(),
        now() + p_ttl,
        now(),
        p_session_id
    )
    ON CONFLICT (post_id) DO UPDATE
       SET user_id      = EXCLUDED.user_id,
           acquired_at  = CASE
                              WHEN post_locks.user_id = EXCLUDED.user_id
                              THEN post_locks.acquired_at
                              ELSE EXCLUDED.acquired_at
                          END,
           expires_at   = EXCLUDED.expires_at,
           heartbeat_at = EXCLUDED.heartbeat_at,
           session_id   = EXCLUDED.session_id;

    -- NULL means "you hold it now".
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
