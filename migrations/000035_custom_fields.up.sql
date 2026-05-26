-- 000035_custom_fields.up.sql
--
-- Custom-fields field groups + per-post meta values (issue #162). One
-- group describes "what extra fields does this post type gain"; one
-- meta row stores the validated blob per (post, group).
--
-- Layered above 000004_posts.up.sql for the FK from
-- post_meta_values.post_id → posts(id) ON DELETE CASCADE; a deleted
-- post takes its custom-field values with it.

-- =============================================================================
-- field_groups
-- =============================================================================

CREATE TABLE IF NOT EXISTS field_groups (
    id           UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    slug         citext NOT NULL UNIQUE,
    title        text NOT NULL,
    post_types   text[] NOT NULL DEFAULT '{}'::text[],
    -- schema is a JSON Schema (draft 2020-12) document constraining
    -- the meta blob. We store it as JSONB rather than TEXT so the
    -- admin can index/sub-query (e.g. "every group with a 'price'
    -- property") without round-tripping every row through the API.
    schema       jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    version      integer NOT NULL DEFAULT 1,
    deleted_at   timestamptz
);

COMMENT ON TABLE  field_groups IS 'JSON-Schema-defined custom field groups attached to one or more post types.';
COMMENT ON COLUMN field_groups.slug IS 'Stable identifier referenced by templates and audit log entries.';
COMMENT ON COLUMN field_groups.schema IS 'Draft 2020-12 JSON Schema document constraining the per-post meta blob.';
COMMENT ON COLUMN field_groups.version IS 'Optimistic-concurrency stamp; bumped on every UPDATE.';

CREATE INDEX IF NOT EXISTS idx_field_groups_post_types
    ON field_groups USING GIN (post_types)
    WHERE deleted_at IS NULL;

-- =============================================================================
-- post_meta_values
-- =============================================================================
--
-- One row per (post, group) tuple. The values column holds the
-- validated JSON blob; nothing in the database enforces the schema
-- (the application validator owns that), but the column is JSONB so
-- the admin can JSONPath into specific fields without re-decoding
-- the whole document.

CREATE TABLE IF NOT EXISTS post_meta_values (
    post_id      UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    group_id     UUID NOT NULL REFERENCES field_groups(id) ON DELETE CASCADE,
    values       jsonb NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, group_id)
);

COMMENT ON TABLE  post_meta_values IS 'Per-post custom-field values keyed by field group.';
COMMENT ON COLUMN post_meta_values.values IS 'JSON blob validated against field_groups.schema at write time.';

CREATE INDEX IF NOT EXISTS idx_post_meta_values_group
    ON post_meta_values (group_id);
