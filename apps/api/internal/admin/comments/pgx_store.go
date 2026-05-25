package comments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxStore is the Postgres-backed Store implementation. It maps the
// admin moderation surface onto the comments table from migration
// 000006: list filters compile to a single SELECT with a (post_id,
// status, created_at DESC) index match where possible, status
// transitions are single-row UPDATE … RETURNING, bulk actions run
// inside a transaction with a row-count guard, and reply inserts
// rely on the comments_set_path BEFORE-INSERT trigger to materialise
// the ltree.
//
// The store does not own the pool — the caller (cmd/server/main.go)
// keeps the pool alive for the lifetime of the process and closes it
// at shutdown. We borrow a pgxQuerier interface so tests can swap in
// a fake; production wiring hands a poolAdapter that forwards to
// *pgxpool.Pool.
type PgxStore struct {
	db pgxQuerier
}

// pgxQuerier is the subset of *pgxpool.Pool the store needs. Kept as
// an interface so the test side can wire a *pgxpool.Pool directly and
// so future code that wants to drive the store inside a larger
// transaction can hand in a pgx.Tx.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgxCmdTag, error)
	Begin(ctx context.Context) (pgxTx, error)
}

// pgxCmdTag is the minimal Exec-result surface — RowsAffected() is
// the only field the store reads. Mirrors the customizer/redirects
// pattern of avoiding a direct pgconn import on the interface.
type pgxCmdTag interface {
	RowsAffected() int64
}

// pgxTx is the minimal transaction surface. We commit/rollback and
// run statements inside one — that is the whole surface area, so the
// interface stays tight.
type pgxTx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgxCmdTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// poolAdapter wraps *pgxpool.Pool so it satisfies pgxQuerier. Mirrors
// the pattern used by the customizer and redirects packages.
type poolAdapter struct {
	pool *pgxpool.Pool
}

func (a poolAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return a.pool.QueryRow(ctx, sql, args...)
}

func (a poolAdapter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return a.pool.Query(ctx, sql, args...)
}

func (a poolAdapter) Exec(ctx context.Context, sql string, args ...any) (pgxCmdTag, error) {
	tag, err := a.pool.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return tagShim{rows: tag.RowsAffected()}, nil
}

func (a poolAdapter) Begin(ctx context.Context) (pgxTx, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return txAdapter{tx: tx}, nil
}

// txAdapter wraps a pgx.Tx in the pgxTx interface so Exec returns the
// shim CommandTag type the store consumes.
type txAdapter struct {
	tx pgx.Tx
}

func (a txAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return a.tx.QueryRow(ctx, sql, args...)
}

func (a txAdapter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return a.tx.Query(ctx, sql, args...)
}

func (a txAdapter) Exec(ctx context.Context, sql string, args ...any) (pgxCmdTag, error) {
	tag, err := a.tx.Exec(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return tagShim{rows: tag.RowsAffected()}, nil
}

func (a txAdapter) Commit(ctx context.Context) error   { return a.tx.Commit(ctx) }
func (a txAdapter) Rollback(ctx context.Context) error { return a.tx.Rollback(ctx) }

type tagShim struct{ rows int64 }

func (t tagShim) RowsAffected() int64 { return t.rows }

// NewPgxStore wraps a *pgxpool.Pool in the production Store. The pool
// is borrowed (not owned); the caller is responsible for closing it
// at shutdown.
func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{db: poolAdapter{pool: pool}}
}

// newPgxStoreFromQuerier is the test-side constructor. Not exported;
// the *_test.go file in this package wires the pool through this
// shim so a unit test can fake just the SQL surface without spinning
// up a real pool.
func newPgxStoreFromQuerier(q pgxQuerier) *PgxStore {
	return &PgxStore{db: q}
}

// selectAdminColumns enumerates the columns the admin list shape
// needs. Centralised so the SELECT and the Scan stay in lockstep —
// drift between the two is the single most common cause of nil-scan
// panics in pgx code.
//
// The LEFT JOIN on posts is unconditional: a comment without a post
// is impossible under the FK, but the JOIN lets us pull post_title
// without a second query. Same shape for the users LEFT JOIN — the
// author may be anonymous (author_user_id NULL), in which case the
// joined display_name comes back NULL and we fall back to the
// snapshotted comments.author_name on the row.
const selectAdminColumns = `
SELECT
    c.id,
    c.post_id,
    COALESCE(p.title, '') AS post_title,
    c.parent_id,
    c.path::text,
    c.author_user_id,
    COALESCE(NULLIF(u.display_name, ''), c.author_name, 'Anonymous') AS author_display_name,
    c.content,
    c.content_format,
    c.status,
    c.created_at,
    c.updated_at
FROM comments c
LEFT JOIN posts p ON p.id = c.post_id
LEFT JOIN users u ON u.id = c.author_user_id
`

// scanAdminComment reads one comment row in admin-list shape. Both
// pgx.Row and pgx.Rows have a compatible Scan signature so a tiny
// interface lets us share the body between Get and the List/Bulk
// paths.
func scanAdminComment(r interface {
	Scan(dest ...any) error
}) (Comment, error) {
	var (
		out      Comment
		parent   *uuid.UUID
		authorID *uuid.UUID
		postID   uuid.UUID
		id       uuid.UUID
	)
	if err := r.Scan(
		&id,
		&postID,
		&out.PostTitle,
		&parent,
		&out.Path,
		&authorID,
		&out.AuthorDisplayName,
		&out.Content,
		&out.ContentFormat,
		&out.Status,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		return Comment{}, err
	}
	out.ID = id.String()
	out.PostID = postID.String()
	if parent != nil {
		out.ParentID = parent.String()
	}
	if authorID != nil {
		out.AuthorUserID = authorID.String()
	}
	if out.AuthorDisplayName == "" {
		out.AuthorDisplayName = "Anonymous"
	}
	return out, nil
}

// List fetches a page of comments matching the filter, newest first.
// The query compiles to a single index probe per filter combination
// (the comments_post_status_created_idx covers post_id+status+
// created_at; status-only walks the comments_pending_idx partial when
// status='pending', else a seq scan that gets clamped by LIMIT). We
// avoid a separate count query — HasNext is computed by asking for
// one row past the page and discarding the extra.
func (s *PgxStore) List(ctx context.Context, f ListFilter) (ListPage, error) {
	page := f.Page
	if page < 1 {
		page = 1
	}
	limit := f.Limit
	if limit < 1 {
		limit = 30
	}
	offset := (page - 1) * limit

	// Build the WHERE clause dynamically. We use $N placeholders and
	// an args slice instead of string interpolation so the SQL is
	// safe even if a future caller leaks a query-param string into a
	// filter field by mistake.
	var (
		wheres []string
		args   []any
	)
	if f.Status != "" {
		args = append(args, string(f.Status))
		wheres = append(wheres, fmt.Sprintf("c.status = $%d", len(args)))
	}
	if f.PostID != "" {
		pid, err := uuid.Parse(f.PostID)
		if err != nil {
			// An unparseable filter UUID yields an empty page; the
			// admin UI shouldn't ever send one, but we fail soft
			// instead of returning an error so the page renders.
			return ListPage{Comments: []Comment{}, HasNext: false}, nil
		}
		args = append(args, pid)
		wheres = append(wheres, fmt.Sprintf("c.post_id = $%d", len(args)))
	}
	if f.UserID != "" {
		uid, err := uuid.Parse(f.UserID)
		if err != nil {
			return ListPage{Comments: []Comment{}, HasNext: false}, nil
		}
		args = append(args, uid)
		wheres = append(wheres, fmt.Sprintf("c.author_user_id = $%d", len(args)))
	}

	where := ""
	if len(wheres) > 0 {
		where = " WHERE " + strings.Join(wheres, " AND ")
	}

	// Ask for limit+1 so we know whether more rows exist without a
	// COUNT(*) round-trip. The extra row is trimmed before return.
	args = append(args, limit+1)
	args = append(args, offset)

	q := selectAdminColumns + where +
		fmt.Sprintf(" ORDER BY c.created_at DESC, c.id DESC LIMIT $%d OFFSET $%d",
			len(args)-1, len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return ListPage{}, fmt.Errorf("admin/comments: list: %w", err)
	}
	defer rows.Close()

	out := make([]Comment, 0, limit)
	for rows.Next() {
		c, err := scanAdminComment(rows)
		if err != nil {
			return ListPage{}, fmt.Errorf("admin/comments: list scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return ListPage{}, fmt.Errorf("admin/comments: list iter: %w", err)
	}

	hasNext := false
	if len(out) > limit {
		out = out[:limit]
		hasNext = true
	}
	return ListPage{Comments: out, HasNext: hasNext}, nil
}

// Get fetches a single comment by ID. Returns ErrNotFound if the row
// is missing or the ID is unparseable — both yield the same observed
// behaviour at the API surface (404), so collapsing the two paths
// keeps the handler simple.
func (s *PgxStore) Get(ctx context.Context, id string) (Comment, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return Comment{}, ErrNotFound
	}
	row := s.db.QueryRow(ctx, selectAdminColumns+" WHERE c.id = $1", uid)
	c, err := scanAdminComment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Comment{}, ErrNotFound
		}
		return Comment{}, fmt.Errorf("admin/comments: get: %w", err)
	}
	return c, nil
}

// UpdateStatus transitions the comment's status. Returns ErrNotFound
// when the row is absent. The comments_touch trigger from migration
// 000006 stamps updated_at; we don't write it explicitly.
//
// Implementation note: we deliberately run the UPDATE and the
// admin-shape SELECT as two statements rather than chaining them in a
// single CTE. Postgres evaluates every sub-query in a statement against
// the same snapshot, so a CTE that UPDATEs then SELECTs would see the
// pre-update row in the SELECT branch — surfacing as a "status didn't
// change" bug in callers. Splitting the work keeps the read path
// reading what was just written.
func (s *PgxStore) UpdateStatus(ctx context.Context, id string, status Status) (Comment, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return Comment{}, ErrNotFound
	}
	var updatedID uuid.UUID
	err = s.db.QueryRow(ctx,
		`UPDATE comments SET status = $2 WHERE id = $1 RETURNING id`,
		uid, string(status),
	).Scan(&updatedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Comment{}, ErrNotFound
		}
		return Comment{}, fmt.Errorf("admin/comments: update status: %w", err)
	}
	row := s.db.QueryRow(ctx, selectAdminColumns+" WHERE c.id = $1", updatedID)
	c, err := scanAdminComment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Comment{}, ErrNotFound
		}
		return Comment{}, fmt.Errorf("admin/comments: update status readback: %w", err)
	}
	return c, nil
}

// Bulk applies status to every ID in a single transaction. Atomic by
// construction: the UPDATE writes to a CTE, we count the affected
// rows against len(ids), and roll back if any ID was unknown.
//
// The "expected = actual" guard is the all-or-nothing contract the
// memory store exposes; preserving it at the DB layer means the
// handler's 422 path triggers consistently regardless of backend.
func (s *PgxStore) Bulk(ctx context.Context, ids []string, status Status) ([]Comment, error) {
	if len(ids) == 0 {
		return []Comment{}, nil
	}
	// Pre-parse all IDs. A single bad UUID rejects the whole batch
	// for the same all-or-nothing reason as a missing row.
	uids := make([]uuid.UUID, 0, len(ids))
	idIdx := make(map[uuid.UUID]int, len(ids))
	for i, id := range ids {
		uid, err := uuid.Parse(id)
		if err != nil {
			return nil, ErrBulkPartial
		}
		uids = append(uids, uid)
		idIdx[uid] = i
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin/comments: bulk begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Single UPDATE … WHERE id = ANY($1) and check the row count.
	const updateSQL = `UPDATE comments SET status = $2 WHERE id = ANY($1::uuid[])`
	tag, err := tx.Exec(ctx, updateSQL, uids, string(status))
	if err != nil {
		return nil, fmt.Errorf("admin/comments: bulk update: %w", err)
	}
	if tag.RowsAffected() != int64(len(uids)) {
		// At least one ID didn't match — roll back and tell the caller
		// the selection was stale.
		return nil, ErrBulkPartial
	}

	// Re-read the rows in admin shape so the response matches the
	// list endpoint exactly.
	rows, err := tx.Query(ctx,
		selectAdminColumns+" WHERE c.id = ANY($1::uuid[])",
		uids)
	if err != nil {
		return nil, fmt.Errorf("admin/comments: bulk select: %w", err)
	}
	collected := make(map[uuid.UUID]Comment, len(uids))
	func() {
		defer rows.Close()
		for rows.Next() {
			c, scanErr := scanAdminComment(rows)
			if scanErr != nil {
				err = fmt.Errorf("admin/comments: bulk scan: %w", scanErr)
				return
			}
			uid, parseErr := uuid.Parse(c.ID)
			if parseErr == nil {
				collected[uid] = c
			}
		}
		err = rows.Err()
	}()
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("admin/comments: bulk commit: %w", err)
	}

	// Re-order the result to match the input order — the handler's
	// response shape preserves the caller's selection order so the
	// UI can correlate updates back to its checkbox state.
	out := make([]Comment, len(uids))
	for i, uid := range uids {
		out[i] = collected[uid]
	}
	_ = idIdx // index map kept for future per-id error reporting
	return out, nil
}

// replyInsertSQL inserts a child comment. The path is left to the
// comments_set_path BEFORE-INSERT trigger from migration 000006 —
// the trigger fires before the row becomes visible, so the INSERT
// doesn't need to compute the ltree itself.
//
// status is 'approved' by default for moderator replies (operator
// endorsement is the point of replying); we set it explicitly here
// rather than relying on the table default ('pending') so a future
// change to the default doesn't silently flip moderator replies into
// the queue.
const replyInsertSQL = `
INSERT INTO comments (post_id, parent_id, author_user_id, author_name, content, status)
SELECT
    parent.post_id,
    $1,
    NULLIF($2::uuid, '00000000-0000-0000-0000-000000000000'::uuid),
    NULLIF($3::text, ''),
    $4,
    'approved'
FROM comments parent
WHERE parent.id = $1
RETURNING id
`

// Reply inserts a child comment under parentID. The path is
// materialised by the comments_set_path BEFORE-INSERT trigger. We
// run the INSERT and follow up with a SELECT in admin-list shape so
// the response carries the joined post_title and display_name fields.
func (s *PgxStore) Reply(ctx context.Context, parentID, authorUserID, authorName, content string) (Comment, error) {
	if strings.TrimSpace(content) == "" {
		return Comment{}, ErrEmptyContent
	}
	pid, err := uuid.Parse(parentID)
	if err != nil {
		return Comment{}, ErrNotFound
	}

	// Author ID is optional — moderator replies typically link to a
	// real user, but the handler may omit it for service-account
	// replies. NULLIF on the SQL side converts a zero UUID to NULL so
	// the FK to users(id) accepts the row.
	var auid uuid.UUID
	if authorUserID != "" {
		parsed, parseErr := uuid.Parse(authorUserID)
		if parseErr != nil {
			// A malformed author ID is a programming error in the
			// handler — log-friendly but not panic-worthy. Surface a
			// generic error so the API returns 500 rather than
			// silently dropping the link.
			return Comment{}, fmt.Errorf("admin/comments: reply: invalid author_user_id: %w", parseErr)
		}
		auid = parsed
	}

	displayName := authorName
	if displayName == "" {
		displayName = "Moderator"
	}

	var newID uuid.UUID
	row := s.db.QueryRow(ctx, replyInsertSQL, pid, auid, displayName, content)
	if err := row.Scan(&newID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The INSERT … SELECT FROM parent yields zero rows when
			// the parent doesn't exist — mirroring the memory store's
			// ErrNotFound.
			return Comment{}, ErrNotFound
		}
		return Comment{}, fmt.Errorf("admin/comments: reply insert: %w", err)
	}

	// Pull the just-inserted row back in admin-list shape so the
	// response includes the joined post_title and the resolved
	// display name from the users table when the author was linked.
	created, err := s.Get(ctx, newID.String())
	if err != nil {
		return Comment{}, fmt.Errorf("admin/comments: reply readback: %w", err)
	}
	// The created_at default is set by the column DEFAULT; surface
	// the value the database has now so the handler's response is
	// authoritative.
	if created.CreatedAt.IsZero() {
		created.CreatedAt = time.Now()
	}
	return created, nil
}
