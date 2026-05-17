-- 000011_search.up.sql
--
-- Postgres full-text search on `posts` (issue #119).
--
-- Adds a precomputed `search_vector` tsvector column, a plpgsql function
-- that builds it from the post's user-facing text fields with weighted
-- ranks, a BEFORE-INSERT-OR-UPDATE trigger that auto-maintains the column
-- on every write, and a GIN index for fast `@@`/`ts_rank_cd` queries.
--
-- Reference: docs/01-core-cms.md §8 (Search), §8.1 (tsvector + weights).
--
-- Why BEFORE INSERT OR UPDATE (not AFTER):
--   The trigger writes to NEW.search_vector before the row is persisted,
--   so the column is materialised in the same write as the data it
--   indexes — no second pass, no async reindex job, no stale window.
--
-- Why all-columns (no `OF title, excerpt, ...`):
--   Keeping the trigger un-narrowed means a future column addition (or
--   a generic `UPDATE posts SET ... WHERE id = ?` that touches `meta`
--   indirectly) cannot silently leave search_vector stale. The cost is
--   a few extra tsvector builds on writes that don't change any of the
--   indexed fields, which is cheap compared to the cost of stale search
--   results. If profiling shows this matters, we can narrow the trigger
--   in a later migration.
--
-- Weights (Postgres FTS A > B > C > D):
--   A — title             (most relevant; matches here outrank body matches)
--   B — excerpt           (curated summary, second priority)
--   C — content_rendered  (full HTML-rendered body; the bulk of the text)
--   D — meta SEO description (`meta -> 'core' -> 'seo' -> 'meta_description'`)
--
-- Language: hardcoded to 'english' for now. The design (§8.3) calls for
-- a per-row `posts.search_language` once we support i18n; that's a
-- follow-up migration. See issue #119 acceptance criteria for the
-- multi-language requirement that the FTS column must support — this
-- migration intentionally ships the single-language baseline so the
-- search API can land in parallel.

-- =============================================================================
-- 1. tsvector column
-- =============================================================================

ALTER TABLE posts
    ADD COLUMN search_vector tsvector;

COMMENT ON COLUMN posts.search_vector IS
    'Materialised FTS index over title (A), excerpt (B), content_rendered (C), and meta SEO description (D). Maintained by posts_search_vector_trg; never write directly. See docs/01-core-cms.md §8.1.';

-- =============================================================================
-- 2. trigger function: build the weighted tsvector
-- =============================================================================

-- coalesce() against '' on every field so a NULL never produces a NULL
-- tsvector — that would make the row invisible to `@@` matches entirely.
-- meta is jsonb; the `->>` operator returns text (or NULL), which is
-- exactly what to_tsvector wants. Chained `->` lets us reach into the
-- nested `core.seo.meta_description` key without a single-string path.
CREATE OR REPLACE FUNCTION posts_search_vector_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.search_vector :=
           setweight(to_tsvector('english', coalesce(NEW.title, '')), 'A')
        || setweight(to_tsvector('english', coalesce(NEW.excerpt, '')), 'B')
        || setweight(to_tsvector('english', coalesce(NEW.content_rendered, '')), 'C')
        || setweight(to_tsvector('english',
             coalesce(NEW.meta -> 'core' -> 'seo' ->> 'meta_description', '')), 'D');
    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION posts_search_vector_update() IS
    'BEFORE INSERT OR UPDATE trigger function: rebuilds posts.search_vector from title (A), excerpt (B), content_rendered (C), and meta.core.seo.meta_description (D) using the English text-search dictionary. Hardcoded language pending posts.search_language (i18n follow-up).';

-- =============================================================================
-- 3. trigger
-- =============================================================================

-- One trigger fires both on insert and on update. Postgres guarantees
-- the BEFORE-row trigger runs before any constraint check, so a row
-- that violates a CHECK on search_vector would be rejected at the same
-- point as any other constraint violation. We don't constrain
-- search_vector, but the ordering matters if a future migration does.
CREATE TRIGGER posts_search_vector_trg
    BEFORE INSERT OR UPDATE ON posts
    FOR EACH ROW
    EXECUTE FUNCTION posts_search_vector_update();

-- =============================================================================
-- 4. GIN index
-- =============================================================================

-- GIN is the right index type for tsvector: it stores postings lists
-- per lexeme, which is what `@@` and `ts_rank_cd` walk. A B-tree would
-- be useless here (tsvectors are not totally ordered in any
-- query-relevant way).
--
-- We don't bother with `CREATE INDEX CONCURRENTLY`: this migration
-- creates the column in the same transaction, so the table is empty of
-- search_vector data anyway and an exclusive lock during build is
-- cheap. On a real production table this would be a separate, manual
-- CONCURRENTLY migration — but that's a deployment-ops concern, not a
-- schema concern.
CREATE INDEX posts_search_vector_gin
    ON posts
    USING gin (search_vector);

-- =============================================================================
-- 5. backfill existing rows
-- =============================================================================

-- The trigger only fires on INSERT or UPDATE; rows that already exist
-- in `posts` (from the 000004 migration onward) have a NULL
-- search_vector and are invisible to search until we populate them.
--
-- We could do `UPDATE posts SET title = title` to nudge the trigger,
-- but that wastes a write per row and produces noise in audit logs /
-- replication. Cleaner: compute the same expression the trigger uses
-- and SET search_vector directly. The function is the source of truth
-- for the formula; keeping the two in sync is a maintenance burden but
-- a small one — and the alternative (a recursive trigger call) is
-- worse.
UPDATE posts
SET search_vector =
       setweight(to_tsvector('english', coalesce(title, '')), 'A')
    || setweight(to_tsvector('english', coalesce(excerpt, '')), 'B')
    || setweight(to_tsvector('english', coalesce(content_rendered, '')), 'C')
    || setweight(to_tsvector('english',
         coalesce(meta -> 'core' -> 'seo' ->> 'meta_description', '')), 'D');
