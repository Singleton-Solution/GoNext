package posts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxStore is the production [Store] backed by Postgres through pgx.
//
// Every query is scoped by post_type so the same pool can simultaneously
// back the posts mount (post_type='post') and the pages mount
// (post_type='page') without leaking rows across them — exactly the
// contract the in-memory [MemoryStore] honors.
//
// The store relies on the schema in migrations/000004_posts.up.sql:
//
//   - touch_updated_at + bump_version triggers handle updated_at and
//     version increments, so the UPDATE statements here NEVER set those
//     columns. We pass the expected version through the WHERE clause and
//     read the post-trigger row from RETURNING.
//   - Soft-delete is `status = 'trash'`, matching the partial unique
//     indexes that exclude trashed rows. There is no deleted_at column
//     in the canonical schema, and Trash here mirrors that exactly.
//   - content_blocks_hash is recomputed (sha256) on every write that
//     touches content_blocks, so handler ETag headers behave identically
//     across the two stores.
//
// The struct holds a borrowed *pgxpool.Pool; the caller owns Close().
type PgxStore struct {
	pool *pgxpool.Pool
}

// NewPgxStore returns a Postgres-backed [Store] over the supplied pool.
// The pool must already be migrated through 000004_posts.up.sql; this
// constructor does NOT verify the schema (the boot path is responsible
// for migrations, not the store).
func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{pool: pool}
}

// buildListSQL assembles the dynamic SELECT used by List. We can't
// pre-declare it as a const because the WHERE clause is conditional on
// which filter fields are populated — the alternative would be carrying
// NULL-or-value args through every call site, which is more error-prone
// than a targeted strings.Builder.
//
// The cursor (`id > $N`) gives us a stable ASC ordering tied to the
// UUID v7 default — the same ordering the in-memory store uses so
// handler tests don't shift behavior when the store swaps.
func buildListSQL(postType string, filter ListFilter) (string, []any) {
	var b strings.Builder
	b.WriteString(`SELECT id, post_type, parent_id, author_id, status, title, slug,
       excerpt, content_blocks, content_blocks_hash, password,
       comment_status, ping_status, menu_order, meta,
       published_at, scheduled_for, created_at, updated_at, version
FROM posts
WHERE post_type = $1`)
	args := []any{postType}

	if filter.Status != "" {
		args = append(args, filter.Status)
		fmt.Fprintf(&b, " AND status = $%d", len(args))
	}
	if filter.AuthorID != "" {
		args = append(args, filter.AuthorID)
		fmt.Fprintf(&b, " AND author_id = $%d", len(args))
	}
	if filter.Search != "" {
		// ILIKE wildcard search on title. The leading '%' rules out a
		// b-tree index — fine for the test substrate and small CMSes;
		// once corpora grow we route this through the FTS path
		// (packages/go/search) and treat List as the structured-only
		// surface.
		args = append(args, "%"+filter.Search+"%")
		fmt.Fprintf(&b, " AND title ILIKE $%d", len(args))
	}
	if filter.After != "" {
		args = append(args, filter.After)
		fmt.Fprintf(&b, " AND id > $%d", len(args))
	}

	// Same shape as MemoryStore: ORDER BY id ASC so the cursor key is
	// stable and predictable, fetch limit+1 so the handler can detect
	// "more pages" without a count.
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	args = append(args, limit+1)
	fmt.Fprintf(&b, " ORDER BY id ASC LIMIT $%d", len(args))
	return b.String(), args
}

// List returns posts of postType that match filter, ordered by id ASC.
// Returns up to filter.Limit+1 rows so the handler can build a cursor.
func (s *PgxStore) List(ctx context.Context, postType string, filter ListFilter) ([]Post, error) {
	sql, args := buildListSQL(postType, filter)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("posts: list query: %w", err)
	}
	defer rows.Close()

	out := make([]Post, 0, filter.Limit+1)
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("posts: list scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("posts: list rows: %w", err)
	}
	return out, nil
}

// selectByIDSQL is the canonical "fetch one" query. Includes every
// column the handler may need to surface; the password column is read
// here because the handler needs it to gate password-protected GETs.
const selectByIDSQL = `
SELECT id, post_type, parent_id, author_id, status, title, slug,
       excerpt, content_blocks, content_blocks_hash, password,
       comment_status, ping_status, menu_order, meta,
       published_at, scheduled_for, created_at, updated_at, version
FROM posts
WHERE id = $1 AND post_type = $2
`

// Get fetches one post by id, scoped to postType. Returns ErrNotFound
// for missing rows and for post_type mismatches (so a /api/v1/posts
// caller can't load a page by guessing its id).
func (s *PgxStore) Get(ctx context.Context, postType, id string) (Post, error) {
	row := s.pool.QueryRow(ctx, selectByIDSQL, id, postType)
	p, err := scanPost(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Post{}, ErrNotFound
		}
		return Post{}, fmt.Errorf("posts: get: %w", err)
	}
	return p, nil
}

// insertSQL writes a new posts row. We let the DB generate id
// (gen_uuid_v7 DEFAULT) and supply only the columns the handler may
// override. Columns missing from the INSERT take their table defaults
// — that matches what MemoryStore does in applyCreate.
//
// content_blocks_hash is computed in Go (sha256 of the raw bytes) so
// the ETag matches what MemoryStore produces. The renderer will later
// own the canonicalised hash for cache-key purposes; until then, a
// raw-bytes sha256 is a faithful in-store mirror.
const insertSQL = `
INSERT INTO posts (
    post_type, parent_id, author_id, status, title, slug, excerpt,
    content_blocks, content_blocks_hash, password,
    comment_status, ping_status, menu_order, meta,
    published_at, scheduled_for
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16
)
RETURNING id, post_type, parent_id, author_id, status, title, slug,
          excerpt, content_blocks, content_blocks_hash, password,
          comment_status, ping_status, menu_order, meta,
          published_at, scheduled_for, created_at, updated_at, version
`

// Create inserts a new post. Returns ErrDuplicateSlug on unique-index
// violation (the partial uniques in 000004 cover both flat and
// hierarchical slug uniqueness).
func (s *PgxStore) Create(ctx context.Context, postType, authorID string, in CreateInput) (Post, error) {
	status := "draft"
	if in.Status != nil {
		status = *in.Status
	}
	title := ""
	if in.Title != nil {
		title = *in.Title
	}
	slug := ""
	if in.Slug != nil {
		slug = *in.Slug
	}
	commentStatus := "open"
	if in.CommentStatus != nil {
		commentStatus = *in.CommentStatus
	}
	pingStatus := "closed"
	if in.PingStatus != nil {
		pingStatus = *in.PingStatus
	}
	menuOrder := 0
	if in.MenuOrder != nil {
		menuOrder = *in.MenuOrder
	}
	contentBlocks := normalizeBlockTree(in.ContentBlocks)
	meta := normalizeMetaObject(in.Meta)
	hash := sha256.Sum256(contentBlocks)

	row := s.pool.QueryRow(ctx, insertSQL,
		postType,
		nullableString(in.ParentID),
		authorID,
		status,
		title,
		slug,
		nullableString(in.Excerpt),
		contentBlocks,
		hash[:],
		nullableString(in.Password),
		commentStatus,
		pingStatus,
		menuOrder,
		meta,
		nullableTime(in.PublishedAt),
		nullableTime(in.ScheduledFor),
	)
	p, err := scanPost(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Post{}, ErrDuplicateSlug
		}
		return Post{}, fmt.Errorf("posts: create: %w", err)
	}
	return p, nil
}

// Update applies a sparse PATCH. The statement is assembled dynamically
// from the non-nil fields in `in`: a static "always-set every column"
// SQL forces tedious type-casting gymnastics through pgx (parent_id is
// uuid, slug is citext, content_blocks is jsonb, etc.), and we'd still
// have to keep "leave alone" sentinels alive at the protocol layer.
//
// The expectedVersion is matched in the WHERE clause so the post-trigger
// row returned by RETURNING is always version+1 — a zero-row outcome
// surfaces as ErrVersionConflict.
//
// We deliberately do NOT touch updated_at / version — the triggers in
// 000004 do that on our behalf.
//
// We resolve the (NotFound, VersionConflict) split by doing a follow-up
// existence check when RETURNING yields zero rows: if the row exists at
// all (any version, scoped to post_type), we report ErrVersionConflict;
// otherwise ErrNotFound. The extra round-trip is paid only on the
// failure path.
func (s *PgxStore) Update(ctx context.Context, postType, id string, expectedVersion int, in UpdateInput) (Post, error) {
	sets := []string{}
	args := []any{id, postType} // $1, $2

	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	if in.ParentID != nil {
		add("parent_id", *in.ParentID)
	}
	if in.Status != nil {
		add("status", *in.Status)
	}
	if in.Title != nil {
		add("title", *in.Title)
	}
	if in.Slug != nil {
		add("slug", *in.Slug)
	}
	if in.Excerpt != nil {
		add("excerpt", *in.Excerpt)
	}
	if in.ContentBlocks != nil {
		blocks := normalizeBlockTree(in.ContentBlocks)
		hash := sha256.Sum256(blocks)
		add("content_blocks", blocks)
		add("content_blocks_hash", hash[:])
	}
	if in.Password != nil {
		add("password", *in.Password)
	}
	if in.CommentStatus != nil {
		add("comment_status", *in.CommentStatus)
	}
	if in.PingStatus != nil {
		add("ping_status", *in.PingStatus)
	}
	if in.MenuOrder != nil {
		add("menu_order", *in.MenuOrder)
	}
	if in.Meta != nil {
		add("meta", normalizeMetaObject(in.Meta))
	}
	if in.PublishedAt != nil {
		add("published_at", *in.PublishedAt)
	}
	if in.ScheduledFor != nil {
		add("scheduled_for", *in.ScheduledFor)
	}

	// Empty-PATCH semantics: the in-memory store still bumps the
	// version (touching no columns through applyUpdate, then ++version
	// explicitly). The schema triggers only fire on a real UPDATE, so
	// we add a no-op assignment to force the trigger when the caller
	// sent only the If-Match header with no body fields. Updating a
	// column to its current value is sufficient for `bump_version`
	// to fire.
	if len(sets) == 0 {
		sets = append(sets, "updated_at = updated_at")
	}

	args = append(args, expectedVersion) // last positional
	versionPos := len(args)

	sql := fmt.Sprintf(`
UPDATE posts SET %s
WHERE id = $1 AND post_type = $2 AND version = $%d
RETURNING id, post_type, parent_id, author_id, status, title, slug,
          excerpt, content_blocks, content_blocks_hash, password,
          comment_status, ping_status, menu_order, meta,
          published_at, scheduled_for, created_at, updated_at, version
`, strings.Join(sets, ", "), versionPos)

	row := s.pool.QueryRow(ctx, sql, args...)
	p, err := scanPost(row)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		if isUniqueViolation(err) {
			return Post{}, ErrDuplicateSlug
		}
		return Post{}, fmt.Errorf("posts: update: %w", err)
	}
	// Zero rows: distinguish NotFound from VersionConflict with a
	// targeted existence probe so the handler returns the right code.
	return Post{}, s.classifyAbsence(ctx, postType, id)
}

// trashSQL flips a row to status='trash'. We share the version /
// post_type guards with Update so the optimistic-concurrency story is
// identical for both flows.
const trashSQL = `
UPDATE posts SET status = 'trash'
WHERE id = $1 AND post_type = $2 AND version = $3
RETURNING id, post_type, parent_id, author_id, status, title, slug,
          excerpt, content_blocks, content_blocks_hash, password,
          comment_status, ping_status, menu_order, meta,
          published_at, scheduled_for, created_at, updated_at, version
`

// Trash is the soft-delete contract. The row stays in place with
// status='trash' so admins can restore it later; the partial unique
// indexes on slug ignore trash rows, so a fresh row can immediately
// reclaim the slug.
//
// As with Update, a zero-row RETURNING means either NotFound or
// VersionConflict; we probe to disambiguate.
func (s *PgxStore) Trash(ctx context.Context, postType, id string, expectedVersion int) (Post, error) {
	row := s.pool.QueryRow(ctx, trashSQL, id, postType, expectedVersion)
	p, err := scanPost(row)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Post{}, fmt.Errorf("posts: trash: %w", err)
	}
	return Post{}, s.classifyAbsence(ctx, postType, id)
}

// classifyAbsence answers the "0 rows updated — was it NotFound or
// VersionConflict?" question by checking whether the (id, post_type)
// pair exists at all. A row exists -> ErrVersionConflict; otherwise
// ErrNotFound.
func (s *PgxStore) classifyAbsence(ctx context.Context, postType, id string) error {
	var exists bool
	const probe = `SELECT EXISTS (SELECT 1 FROM posts WHERE id = $1 AND post_type = $2)`
	if err := s.pool.QueryRow(ctx, probe, id, postType).Scan(&exists); err != nil {
		return fmt.Errorf("posts: classify absence: %w", err)
	}
	if exists {
		return ErrVersionConflict
	}
	return ErrNotFound
}

// scanRowOrRows is the tiny interface both pgx.Row and pgx.Rows expose.
// Lets scanPost serve QueryRow and Query callers from the same body.
type scanRowOrRows interface {
	Scan(dest ...any) error
}

// scanPost reads one posts row into a Post. The unexported hash and
// password fields are populated here (rather than via toPost) because
// scanning is the only place we materialize them.
func scanPost(r scanRowOrRows) (Post, error) {
	var (
		p             Post
		parentID      *string
		excerpt       *string
		contentBlocks []byte
		hash          []byte
		password      *string
		meta          []byte
		publishedAt   *time.Time
		scheduledFor  *time.Time
	)
	if err := r.Scan(
		&p.ID,
		&p.PostType,
		&parentID,
		&p.AuthorID,
		&p.Status,
		&p.Title,
		&p.Slug,
		&excerpt,
		&contentBlocks,
		&hash,
		&password,
		&p.CommentStatus,
		&p.PingStatus,
		&p.MenuOrder,
		&meta,
		&publishedAt,
		&scheduledFor,
		&p.CreatedAt,
		&p.UpdatedAt,
		&p.Version,
	); err != nil {
		return Post{}, err
	}

	p.ParentID = parentID
	p.Excerpt = excerpt
	p.PublishedAt = publishedAt
	p.ScheduledFor = scheduledFor

	if len(contentBlocks) > 0 {
		p.ContentBlocks = json.RawMessage(contentBlocks)
	} else {
		p.ContentBlocks = json.RawMessage("[]")
	}
	if len(meta) > 0 {
		p.Meta = json.RawMessage(meta)
	} else {
		p.Meta = json.RawMessage("{}")
	}
	p.hash = hash
	p.password = password
	p.Protected = password != nil && *password != ""
	return p, nil
}

// normalizeBlockTree returns a JSON byte slice safe to write to the
// content_blocks column. A nil or empty input becomes '[]' so we never
// pass a zero-length jsonb value to pgx (which Postgres rejects as
// invalid JSON).
func normalizeBlockTree(in json.RawMessage) []byte {
	if len(in) == 0 {
		return []byte("[]")
	}
	// Defensive copy: callers may mutate the original after we hand it
	// off to pgx (which buffers it on the connection).
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// normalizeMetaObject returns a JSON byte slice safe to write to the
// meta column. A nil or empty input becomes '{}' for the same reason
// as normalizeBlockTree.
func normalizeMetaObject(in json.RawMessage) []byte {
	if len(in) == 0 {
		return []byte("{}")
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// nullableString returns the pointed-to value when p is non-nil; nil
// otherwise. Used to hand "client omitted" through pgx as NULL.
func nullableString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// nullableTime is the time-shaped twin of nullableString.
func nullableTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return *p
}

// isUniqueViolation reports whether err is a Postgres unique-index
// violation (SQLSTATE 23505). We inspect *pgconn.PgError directly here
// rather than string-matching — the import is already on the api
// module's path through pgx itself, so the call is free.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
