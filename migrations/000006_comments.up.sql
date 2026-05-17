-- 000006_comments.up.sql
--
-- Threaded comments table.
--
-- Companion of issue #70. Builds the `comments` surface that doc 01
-- §6 specifies: a single table per post, threading via materialized
-- `ltree` paths, support for both authenticated and anonymous
-- commenters, a four-state moderation enum, and the columns the
-- spam/abuse pipeline (doc 06 §… and pre_comment hook in doc 02)
-- writes into.
--
-- Why a separate table instead of stuffing comments into `posts` like
-- WordPress does:
--   - polymorphism in `wp_posts` makes every query that "actually
--     wants a comment" carry a `post_type='comment'` predicate;
--   - comments need different indexes (per-post recency, GiST on
--     thread path) than posts;
--   - dropping a post should hard-delete its comments, while dropping
--     a user should leave the comment body but null the author link.
--     Separate tables let each FK pick its own ON DELETE rule cleanly.
--
-- Threading model (doc 01 §6.2):
--   `parent_id` is the immediate parent; `path` is the materialized
--   ancestry as an `ltree` value. Each label in the path is the
--   comment's UUID with hyphens turned into underscores (ltree labels
--   match /[A-Za-z0-9_]+/, max 256 chars; a UUID with hyphens stripped
--   sits well within that). Building the path from UUIDs (rather than
--   doc 01's example "01.04.02" zero-padded sequence numbers) avoids
--   a sibling-counting query on every insert and sidesteps the
--   race-condition window between "count my siblings" and "claim a
--   sequence number" under contention. The cost is uglier path
--   literals; we never show them to humans.
--
-- Subtree queries: `WHERE path <@ <ancestor_path>` becomes a single
-- GiST index lookup, which is the read pattern the comment view uses
-- when it collapses or expands a thread.
--
-- Dependencies (assumed to land before this migration applies):
--   * 000001_init       — `ltree` extension, `citext` extension,
--                          `gen_uuid_v7()`.
--   * 000002_users      — `users(id)` for `author_user_id` FK.
--   * 000004_posts      — `posts(id)` for `post_id` FK.

-- =============================================================================
-- comments
-- =============================================================================

CREATE TABLE comments (
    id                  UUID PRIMARY KEY DEFAULT gen_uuid_v7(),

    -- Owning post. Hard-delete cascade: when a post is deleted the
    -- thread goes with it. "Trash" on a post is a status change, not
    -- a delete, so this only fires on permanent removal.
    post_id             UUID NOT NULL
                            REFERENCES posts(id) ON DELETE CASCADE,

    -- Immediate parent. NULL for top-level comments. Hard-delete
    -- cascade keeps orphan rows from accumulating when a moderator
    -- nukes a subtree — though in practice we prefer status='trash'
    -- on the root and let the UI hide descendants.
    parent_id           UUID
                            REFERENCES comments(id) ON DELETE CASCADE,

    -- Materialized thread path. NOT NULL; the BEFORE-INSERT trigger
    -- below fills it before the row is visible. See file header for
    -- the encoding (UUID with hyphens → underscores, one label per
    -- ancestor including self).
    path                ltree NOT NULL,

    -- Author link. SET NULL on user delete: we keep the comment body
    -- so threads don't collapse when an account is closed, but the
    -- comment becomes effectively anonymous from that point. The
    -- denormalized author_name/email below preserve the as-of-write
    -- snapshot in case the UI wants to render "by Jane Doe" even
    -- after the account is gone.
    author_user_id      UUID
                            REFERENCES users(id) ON DELETE SET NULL,

    -- Denormalized author identity.
    --   author_name : display name. Required for anonymous comments;
    --                  also stored for authenticated ones as a
    --                  point-in-time snapshot.
    --   author_email: anonymous-only contact. citext because
    --                  Gravatar lookups and dedupe-by-email both want
    --                  case-insensitive equality. Never returned via
    --                  the public API (see doc 13 §… on PII).
    --   author_url  : optional URL the commenter typed; sanitized
    --                  before render.
    author_name         text,
    author_email        citext,
    author_url          text,

    -- Network identity at write time. Used by the spam scorer
    -- (pre_comment hook, doc 02) and by the rate limiter. Per doc 06
    -- §…, the full address is retained for at most 90 days; a
    -- background job anonymizes to /24 (v4) or /64 (v6) after that.
    -- We store as `inet` to keep that masking a one-line `set_masklen`
    -- update rather than parsing strings.
    author_ip           inet,
    author_user_agent   text,

    -- Body.
    --   content        : raw text as submitted (after HTML
    --                     sanitization at write time — see doc 13
    --                     and PR #284 on the bluemonday UGC profile).
    --                     Storing the sanitized form rather than raw
    --                     means re-render is cheap and no plugin can
    --                     reintroduce <script> via a render hook.
    --   content_format : tells the renderer how to interpret
    --                     `content`. Defaults to 'html' because the
    --                     sanitizer already produced safe HTML; the
    --                     'plain' and 'markdown' values exist for
    --                     importers (doc 08) that bring in legacy
    --                     content without round-tripping through
    --                     the sanitizer.
    content             text NOT NULL,
    content_format      text NOT NULL DEFAULT 'html'
                            CHECK (content_format IN ('html', 'plain', 'markdown')),

    -- Moderation state.
    --   pending  — awaits a human or auto-approval rule (default for
    --              non-trusted authors).
    --   approved — visible on the post.
    --   spam     — caught by the scorer; not visible, kept for
    --              corpus training.
    --   trash    — soft-deleted; kept for undo, GC'd after retention.
    -- A CHECK constraint enforces the enum rather than a TYPE because
    -- the value set is small and may grow ('archived', 'flagged'),
    -- and ALTER TYPE … ADD VALUE is awkward inside migrations.
    status              text NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'approved', 'spam', 'trash')),

    -- Spam scorer output (0..100 conventionally, but real because
    -- some scorers emit log-odds). NULL means "no scorer ran" —
    -- which is different from "ran and scored zero" and matters when
    -- a plugin is later enabled.
    spam_score          real,

    -- Upvotes minus downvotes. Optional surface (only some themes
    -- expose it). Integer rather than smallint because the cap was
    -- arbitrary and Postgres stores both in 4 bytes when not in a
    -- packed array.
    karma               integer NOT NULL DEFAULT 0,

    -- Extension bucket. Plugins read/write through a typed accessor
    -- (doc 01 §4 on the meta API) so we don't need per-key columns.
    meta                jsonb NOT NULL DEFAULT '{}'::jsonb,

    -- Lifecycle timestamps and optimistic-concurrency counter. The
    -- touch_updated_at and bump_version triggers (from doc 01 §10.14;
    -- created by migration 000001 when the helpers ship there, or by
    -- the earlier table migrations that depend on them) are attached
    -- below.
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    version             integer NOT NULL DEFAULT 1
);

COMMENT ON TABLE comments IS
    'Threaded comments per post. Threading via ltree path materialised by trigger. See docs/01-core-cms.md §6.';

COMMENT ON COLUMN comments.path IS
    'Materialised ancestry as ltree. Each label is the comment UUID with hyphens replaced by underscores. Maintained by the comments_set_path trigger.';

COMMENT ON COLUMN comments.author_ip IS
    'Author IP at write time. Anonymised to /24 (v4) or /64 (v6) after 90 days per docs/06-auth-permissions.md.';

COMMENT ON COLUMN comments.content IS
    'Sanitised HTML (or markdown/plain per content_format). Sanitisation happens at write time using the bluemonday UGC profile (see PR #284).';

-- =============================================================================
-- Path-maintenance trigger
-- =============================================================================
--
-- BEFORE INSERT OR UPDATE OF parent_id: compute `path` from the
-- parent's path plus the new row's id. A self-row label is appended
-- so that subtree queries on a comment include the comment itself
-- (`WHERE path <@ <comment.path>` returns the comment and all its
-- descendants).
--
-- ltree label constraints: must match /[A-Za-z0-9_]+/, max 256 bytes.
-- UUIDs are 36 chars with four hyphens. We translate `-` to `_` so
-- the UUID is a legal label without losing the original characters
-- (the reverse mapping is unambiguous because UUIDs don't contain
-- underscores natively).
--
-- We use the row's `id` (already DEFAULT-populated by the time the
-- BEFORE trigger fires; gen_uuid_v7() is evaluated when the row is
-- constructed) rather than calling gen_uuid_v7() again, which would
-- desync `id` and the last label of `path`.
--
-- The UPDATE branch fires only when parent_id changes. Reparenting
-- a comment is rare but supported (merging accidental top-level
-- replies into the right thread). When it does fire, descendants'
-- paths become stale — we recompute them in the same trigger via a
-- cascading UPDATE that re-triggers itself row-by-row. For typical
-- thread depths (≤6 per doc 01 §6.2) this is cheap; deeper trees
-- pay O(subtree-size) on a reparent, which is the right cost shape.

CREATE OR REPLACE FUNCTION comments_set_path() RETURNS trigger AS $$
DECLARE
    parent_path ltree;
    self_label  text;
BEGIN
    -- Build this row's label from its id. gen_uuid_v7() has already
    -- populated NEW.id by the time a BEFORE INSERT trigger sees it
    -- (column DEFAULTs are evaluated during row construction, before
    -- BEFORE triggers).
    self_label := replace(NEW.id::text, '-', '_');

    IF NEW.parent_id IS NULL THEN
        NEW.path := text2ltree(self_label);
    ELSE
        SELECT path INTO parent_path
        FROM comments
        WHERE id = NEW.parent_id;

        IF parent_path IS NULL THEN
            -- Parent row missing or its path not yet materialised.
            -- The FK constraint catches the "missing parent" case at
            -- statement end; this branch fires only on a same-
            -- statement multi-row insert where the parent is in the
            -- same batch and ordered after the child. Reject loudly
            -- rather than silently producing a malformed path.
            RAISE EXCEPTION
                'comments_set_path: parent comment % has no materialised path',
                NEW.parent_id;
        END IF;

        NEW.path := parent_path || text2ltree(self_label);
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION comments_set_path() IS
    'BEFORE INSERT/UPDATE OF parent_id trigger for comments.path. Computes path = parent.path || label(self.id) where label() replaces hyphens with underscores.';

CREATE TRIGGER comments_set_path_ins
    BEFORE INSERT ON comments
    FOR EACH ROW
    EXECUTE FUNCTION comments_set_path();

CREATE TRIGGER comments_set_path_upd
    BEFORE UPDATE OF parent_id ON comments
    FOR EACH ROW
    WHEN (NEW.parent_id IS DISTINCT FROM OLD.parent_id)
    EXECUTE FUNCTION comments_set_path();

-- Cascade reparenting to descendants. AFTER UPDATE so the new
-- NEW.path is visible to the recursive UPDATE; we touch each
-- descendant individually so the BEFORE trigger above recomputes
-- its path from its (now-updated) parent.
CREATE OR REPLACE FUNCTION comments_reparent_descendants() RETURNS trigger AS $$
BEGIN
    -- Re-poke direct children. Each child's BEFORE trigger will
    -- recompute its own path, and the AFTER trigger here will
    -- recurse into grandchildren. This is O(subtree); for the
    -- 6-level cap in doc 01 §6.2 it stays well under a millisecond.
    UPDATE comments
    SET parent_id = parent_id  -- no-op write to fire the BEFORE trigger
    WHERE parent_id = NEW.id;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION comments_reparent_descendants() IS
    'AFTER UPDATE OF parent_id trigger for comments. Re-fires the path-maintenance trigger on direct children so a reparent cascades through the subtree.';

CREATE TRIGGER comments_reparent_descendants
    AFTER UPDATE OF parent_id ON comments
    FOR EACH ROW
    WHEN (NEW.parent_id IS DISTINCT FROM OLD.parent_id)
    EXECUTE FUNCTION comments_reparent_descendants();

-- =============================================================================
-- updated_at / version maintenance
-- =============================================================================
--
-- The touch_updated_at() and bump_version() functions are defined in
-- doc 01 §10.14 and created by migration 000001 (or by the first
-- table migration that depends on them — they exist by the time
-- this file applies, by precedence of the migrations referenced in
-- the file header). We attach both triggers here so comments
-- participate in the standard lifecycle conventions.
--
-- We guard with IF NOT EXISTS-style checks via CREATE OR REPLACE on
-- the functions only if they're missing — but the migration sequence
-- guarantees presence, so a straight CREATE TRIGGER is correct.
-- If a downstream forks the migration ordering they can move the
-- helper creation into 000001.

CREATE TRIGGER comments_touch
    BEFORE UPDATE ON comments
    FOR EACH ROW
    EXECUTE FUNCTION touch_updated_at();

CREATE TRIGGER comments_version
    BEFORE UPDATE ON comments
    FOR EACH ROW
    EXECUTE FUNCTION bump_version();

-- =============================================================================
-- Indexes
-- =============================================================================

-- Per-post list, newest first, filtered by moderation state. This is
-- the public-facing "show me approved comments on this post" query
-- and the admin "show me pending comments on this post" query. Both
-- start with (post_id, status); the trailing created_at DESC lets us
-- avoid a sort on the typical "newest 50" surface.
CREATE INDEX comments_post_status_created_idx
    ON comments (post_id, status, created_at DESC);

-- GiST on the materialised path. Powers `path <@ ancestor`
-- (descendants), `path @> descendant` (ancestors), and `nlevel(path)`
-- depth queries. The exact subtree query in doc 01 §6.2 is a single
-- index lookup against this.
CREATE INDEX comments_path_idx
    ON comments USING gist (path);

-- "Comments by this user, anywhere". The partial predicate keeps the
-- index small — anonymous comments dominate volume on most sites and
-- have no author_user_id to look up.
CREATE INDEX comments_author_user_idx
    ON comments (author_user_id)
    WHERE author_user_id IS NOT NULL;

-- Moderation queue: "show me everything awaiting review, newest first".
-- Partial WHERE status='pending' makes this index small (it tracks
-- only the actionable working set) and cheap to maintain (a comment
-- leaves the index as soon as a moderator approves or trashes it).
CREATE INDEX comments_pending_idx
    ON comments (created_at DESC)
    WHERE status = 'pending';
