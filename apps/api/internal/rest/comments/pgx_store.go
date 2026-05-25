package comments

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxStore is the Postgres-backed Store for the public comments
// surface. It maps onto the comments table from migration 000006
// using ltree path ordering for natural thread rendering:
//
//   - List filters by post_id and status='approved', ORDER BY path
//     ASC so a thread reads parent-then-child top to bottom.
//   - Submit inserts a row and lets the comments_set_path BEFORE-
//     INSERT trigger materialise the ltree from the parent_id.
//   - PostExists is a single SELECT 1 against posts (the public
//     surface needs the foreign key check before it touches comments
//     so an unknown post 404s without leaving a half-written row).
//   - CommentsByIP supports the rate limiter; the query walks the
//     author_ip column with a window predicate.
//
// The store does not own the pool — the caller wires it once at boot
// and closes it on shutdown.
type PgxStore struct {
	db pgxQuerier
}

// pgxQuerier is the SQL surface the store consumes. Mirrors the
// admin package's pattern: a thin interface so tests can wire either
// a real pool or a fake.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgxCmdTag, error)
}

type pgxCmdTag interface {
	RowsAffected() int64
}

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

type tagShim struct{ rows int64 }

func (t tagShim) RowsAffected() int64 { return t.rows }

// NewPgxStore wraps a *pgxpool.Pool in the production Store.
func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{db: poolAdapter{pool: pool}}
}

// newPgxStoreFromQuerier is the test-side constructor.
func newPgxStoreFromQuerier(q pgxQuerier) *PgxStore {
	return &PgxStore{db: q}
}

// selectPublicColumns is the public-surface projection. We
// deliberately omit PII (email, ip, user_agent) and moderation
// telemetry (spam_score, status) because they leak through the JSON
// otherwise. The LEFT JOIN on users resolves the display_name for
// linked authors; anonymous commenters fall back to the snapshotted
// comments.author_name column.
const selectPublicColumns = `
SELECT
    c.id,
    c.post_id,
    c.parent_id,
    c.path::text,
    nlevel(c.path)::int AS depth,
    COALESCE(NULLIF(u.display_name, ''), c.author_name, 'Anonymous') AS author_display_name,
    c.content,
    c.created_at
FROM comments c
LEFT JOIN users u ON u.id = c.author_user_id
`

// scanPublicComment reads one comment in public-list shape.
func scanPublicComment(r interface {
	Scan(dest ...any) error
}) (Comment, error) {
	var (
		out      Comment
		parent   *uuid.UUID
		postID   uuid.UUID
		id       uuid.UUID
		depth    int32
	)
	if err := r.Scan(
		&id,
		&postID,
		&parent,
		&out.Path,
		&depth,
		&out.AuthorDisplayName,
		&out.Content,
		&out.CreatedAt,
	); err != nil {
		return Comment{}, err
	}
	out.ID = id.String()
	out.PostID = postID.String()
	if parent != nil {
		out.ParentID = parent.String()
	}
	out.Depth = int(depth)
	if out.AuthorDisplayName == "" {
		out.AuthorDisplayName = "Anonymous"
	}
	return out, nil
}

// List returns approved comments on f.PostID, ordered by path
// ascending so the thread renders parent-before-child. Cursor
// pagination is on (path, id) — the path embeds a v7 UUID label, so
// in practice path uniqueness alone is enough, but the (path, id)
// tuple guarantees stability against any future denser scheme.
//
// The query uses the comments_post_status_created_idx (post_id,
// status, created_at) index to scope rows, then the GiST path index
// (comments_path_idx) for the ORDER BY. limit+1 gives us HasNext
// without a separate count.
func (s *PgxStore) List(ctx context.Context, f ListFilter) (ListPage, error) {
	if f.PostID == "" {
		return ListPage{Comments: []Comment{}, HasNext: false}, nil
	}
	pid, err := uuid.Parse(f.PostID)
	if err != nil {
		return ListPage{Comments: []Comment{}, HasNext: false}, nil
	}
	limit := f.Limit
	if limit < 1 {
		limit = 50
	}

	// Build the WHERE clause. Cursor predicate uses a single (path, id)
	// tuple comparison so the index walk stays a range scan. Postgres
	// supports tuple comparison natively; we emit
	//   (c.path::text, c.id::text) > ($3, $4)
	// which lets the planner walk the GiST/index ordering.
	args := []any{
		pid,
		string(StatusApproved),
	}
	where := "c.post_id = $1 AND c.status = $2"
	if f.AfterPath != "" {
		args = append(args, f.AfterPath)
		afterID := f.AfterID
		if afterID == "" {
			afterID = ""
		}
		args = append(args, afterID)
		where += fmt.Sprintf(" AND (c.path::text, c.id::text) > ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, limit+1)

	q := selectPublicColumns +
		" WHERE " + where +
		fmt.Sprintf(" ORDER BY c.path ASC, c.id ASC LIMIT $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return ListPage{}, fmt.Errorf("rest/comments: list: %w", err)
	}
	defer rows.Close()

	out := make([]Comment, 0, limit)
	for rows.Next() {
		c, err := scanPublicComment(rows)
		if err != nil {
			return ListPage{}, fmt.Errorf("rest/comments: list scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return ListPage{}, fmt.Errorf("rest/comments: list iter: %w", err)
	}

	hasNext := false
	if len(out) > limit {
		out = out[:limit]
		hasNext = true
	}
	return ListPage{Comments: out, HasNext: hasNext}, nil
}

// submitInsertSQL inserts a new comment. The path is left to the
// comments_set_path BEFORE-INSERT trigger from migration 000006 — we
// just hand it the post_id, optional parent_id, and the author/content
// fields. Returning the row in public-list shape avoids a follow-up
// SELECT.
//
// We don't write status explicitly here because the table default is
// 'pending' — but the public surface needs to set status from the
// classifier, so the SQL accepts an explicit status parameter.
const submitInsertSQL = `
INSERT INTO comments (
    post_id, parent_id, author_user_id, author_name, author_email,
    author_ip, author_user_agent, content, status
) VALUES (
    $1, NULLIF($2::uuid, '00000000-0000-0000-0000-000000000000'::uuid),
    NULLIF($3::uuid, '00000000-0000-0000-0000-000000000000'::uuid),
    $4, NULLIF($5::text, '')::citext,
    NULLIF($6::text, '')::inet, NULLIF($7::text, ''), $8, $9
)
RETURNING id, created_at
`

// Submit inserts a new comment row. Path materialisation is handled
// by the BEFORE-INSERT trigger; we follow up with a SELECT in
// public-list shape to populate the response.
func (s *PgxStore) Submit(ctx context.Context, in SubmitInput, initialStatus Status) (Comment, error) {
	if strings.TrimSpace(in.Content) == "" {
		return Comment{}, ErrEmptyContent
	}
	if in.PostID == "" {
		return Comment{}, ErrNotFound
	}
	postID, err := uuid.Parse(in.PostID)
	if err != nil {
		return Comment{}, ErrNotFound
	}

	// Parent validation. If a parent_id is supplied we look it up to
	// confirm it exists AND belongs to the same post — the FK alone
	// can't tell us "wrong post" so we do the join here.
	var parentID uuid.UUID
	if in.ParentID != "" {
		pid, perr := uuid.Parse(in.ParentID)
		if perr != nil {
			return Comment{}, ErrNotFound
		}
		var parentPostID uuid.UUID
		err := s.db.QueryRow(ctx,
			`SELECT post_id FROM comments WHERE id = $1`, pid,
		).Scan(&parentPostID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Comment{}, ErrNotFound
			}
			return Comment{}, fmt.Errorf("rest/comments: submit parent lookup: %w", err)
		}
		if parentPostID != postID {
			return Comment{}, ErrParentMismatch
		}
		parentID = pid
	}

	var authorID uuid.UUID
	if in.AuthorUserID != "" {
		uid, uerr := uuid.Parse(in.AuthorUserID)
		if uerr == nil {
			authorID = uid
		}
		// A malformed author UUID falls through as anonymous — the
		// handler's responsibility is to keep its principal data
		// clean; the store doesn't reject the comment over it.
	}

	// IP normalisation: an empty string passes NULLIF, a malformed
	// address would fail the inet cast. We pre-validate so an
	// upstream proxy header with garbage doesn't 500 the request.
	ipArg := in.AuthorIP
	if ipArg != "" && net.ParseIP(ipArg) == nil {
		ipArg = ""
	}

	displayName := in.AuthorName
	if displayName == "" {
		displayName = "Anonymous"
	}

	var (
		newID     uuid.UUID
		createdAt time.Time
	)
	err = s.db.QueryRow(ctx, submitInsertSQL,
		postID,
		parentID,
		authorID,
		displayName,
		in.AuthorEmail,
		ipArg,
		in.AuthorUserAgent,
		in.Content,
		string(initialStatus),
	).Scan(&newID, &createdAt)
	if err != nil {
		// A bad FK on post_id surfaces here as a 23503 unique-violation
		// flavour error; we translate to ErrNotFound so the handler
		// returns the right status.
		if isFKViolation(err) {
			return Comment{}, ErrNotFound
		}
		return Comment{}, fmt.Errorf("rest/comments: submit insert: %w", err)
	}

	// Read back in public-list shape so the response carries the
	// resolved display_name from the users join (if logged-in) and
	// the materialised path from the trigger.
	row := s.db.QueryRow(ctx, selectPublicColumns+" WHERE c.id = $1", newID)
	c, err := scanPublicComment(row)
	if err != nil {
		return Comment{}, fmt.Errorf("rest/comments: submit readback: %w", err)
	}
	return c, nil
}

// PostExists reports whether the given post id is present in the
// posts table. Returns nil error when the post is absent — only
// failure to query yields an error.
func (s *PgxStore) PostExists(ctx context.Context, postID string) (bool, error) {
	pid, err := uuid.Parse(postID)
	if err != nil {
		return false, nil
	}
	var exists bool
	err = s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM posts WHERE id = $1)`, pid,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("rest/comments: post exists: %w", err)
	}
	return exists, nil
}

// CommentsByIP returns the number of comments the given IP has
// submitted since `since`. Uses the author_ip column directly; the
// table doesn't have an explicit (ip, created_at) index because the
// rate limiter is best-effort and the volume is low.
//
// Empty IP returns zero without touching the database.
func (s *PgxStore) CommentsByIP(ctx context.Context, ip string, since time.Time) (int, error) {
	if ip == "" {
		return 0, nil
	}
	if net.ParseIP(ip) == nil {
		return 0, nil
	}
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM comments WHERE author_ip = $1::inet AND created_at >= $2`,
		ip, since,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("rest/comments: comments-by-ip: %w", err)
	}
	return n, nil
}

// isFKViolation returns true when err looks like a Postgres
// foreign-key violation (SQLState 23503). The check is string-based
// to avoid importing pgconn — same trade-off as the redirects
// package.
func isFKViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23503") ||
		strings.Contains(msg, "violates foreign key constraint")
}
