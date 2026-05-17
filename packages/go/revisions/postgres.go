package revisions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresDefaultLimit is the default cap when Filter.Limit is zero.
const postgresDefaultLimit = 100

// postgresMaxLimit is the hard upper bound on List, regardless of
// what the caller asked for. The editor's revision-browse UI paginates
// at a much smaller page size; this guard exists for a buggy or
// curious caller asking for the world.
const postgresMaxLimit = 1000

// PgxQuerier is the subset of *pgxpool.Pool used by PostgresStore.
//
// Exposing the interface (rather than taking *pgxpool.Pool directly)
// keeps PostgresStore testable with a pgxmock-style fake and lets
// callers swap in a pgx.Tx when they need to write a revision as
// part of a larger transaction (e.g. the post-publish flow that
// flips status AND writes a publish revision atomically).
type PgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PostgresStore writes revisions via parameterized SQL to the
// post_revisions table documented in docs/01-core-cms.md §10.6.
//
// The CREATE TABLE migration is owned by a downstream issue — this
// store locks the column contract so the Go side already speaks SQL
// against the documented columns. INSERTs on a host where
// post_revisions does not yet exist will fail with the usual pgx
// UndefinedTable error; that's expected and intentional.
type PostgresStore struct {
	db PgxQuerier

	// NowFunc, if set, replaces time.Now for CreatedAt defaults. Used
	// only when the caller leaves CreatedAt zero; supplied times pass
	// through.
	NowFunc func() time.Time
}

// NewPostgresStore wraps a *pgxpool.Pool. The pool's lifecycle is the
// caller's responsibility — the store does not call Close.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{db: pool}
}

// NewPostgresStoreWithQuerier is the test seam: it lets callers swap
// in a fake or a pgx.Tx. Production code should use NewPostgresStore.
func NewPostgresStoreWithQuerier(q PgxQuerier) *PostgresStore {
	return &PostgresStore{db: q}
}

func (s *PostgresStore) now() time.Time {
	if s.NowFunc != nil {
		return s.NowFunc().UTC()
	}
	return time.Now().UTC()
}

// insertSnapshotSQL is the canonical INSERT for a snapshot row.
// Column list is locked against docs/01-core-cms.md §10.6 plus the
// extra denormalized fields (excerpt, content_blocks_hash) the editor
// needs for its revisions-list UI.
const insertSnapshotSQL = `
INSERT INTO post_revisions (
    post_id,
    author_id,
    created_at,
    kind,
    snapshot,
    title,
    excerpt,
    content_blocks_hash,
    comment
) VALUES (
    $1, NULLIF($2::TEXT, '')::UUID, $3, $4,
    $5, NULLIF($6, ''), NULLIF($7, ''),
    NULLIF($8, '\x'::BYTEA), NULLIF($9, '')
) RETURNING id::TEXT
`

// insertDeltaSQL is the canonical INSERT for a delta row.
const insertDeltaSQL = `
INSERT INTO post_revisions (
    post_id,
    author_id,
    created_at,
    kind,
    delta_from,
    delta,
    title,
    excerpt,
    content_blocks_hash,
    comment
) VALUES (
    $1, NULLIF($2::TEXT, '')::UUID, $3, $4,
    $5, $6, NULLIF($7, ''), NULLIF($8, ''),
    NULLIF($9, '\x'::BYTEA), NULLIF($10, '')
) RETURNING id::TEXT
`

// selectByIDSQL reads one revision by primary key.
const selectByIDSQL = `
SELECT
    id::TEXT,
    post_id::TEXT,
    COALESCE(author_id::TEXT, ''),
    created_at,
    kind,
    COALESCE(title, ''),
    COALESCE(excerpt, ''),
    content_blocks_hash,
    COALESCE(delta_from::TEXT, ''),
    delta,
    snapshot,
    COALESCE(comment, '')
FROM post_revisions
WHERE id = $1
`

// listSQL pulls a page of revisions for one post with optional
// filters folded in via NULL passthrough. NULL-or-equal predicates
// keep the statement plan stable across filter combinations.
const listSQL = `
SELECT
    id::TEXT,
    post_id::TEXT,
    COALESCE(author_id::TEXT, ''),
    created_at,
    kind,
    COALESCE(title, ''),
    COALESCE(excerpt, ''),
    content_blocks_hash,
    COALESCE(delta_from::TEXT, ''),
    delta,
    snapshot,
    COALESCE(comment, '')
FROM post_revisions
WHERE post_id = $1
  AND ($2::TIMESTAMPTZ IS NULL OR created_at >= $2)
  AND ($3::TIMESTAMPTZ IS NULL OR created_at <= $3)
  AND ($4 = '' OR author_id::TEXT = $4)
  AND ($5 = '' OR kind::TEXT = $5)
ORDER BY created_at DESC, id DESC
LIMIT $6
`

// latestByKindSQL returns the single most recent revision of a given
// kind for a post. Used by the editor's "compare to last publish"
// view and by the autosave-restore flow on editor open.
const latestByKindSQL = `
SELECT
    id::TEXT,
    post_id::TEXT,
    COALESCE(author_id::TEXT, ''),
    created_at,
    kind,
    COALESCE(title, ''),
    COALESCE(excerpt, ''),
    content_blocks_hash,
    COALESCE(delta_from::TEXT, ''),
    delta,
    snapshot,
    COALESCE(comment, '')
FROM post_revisions
WHERE post_id = $1 AND kind = $2
ORDER BY created_at DESC, id DESC
LIMIT 1
`

// countByPostSQL is used by Save to decide snapshot vs delta. We
// could read all rows for the post and count in Go, but that's
// quadratic on a long-lived post; a single COUNT + a "latest
// snapshot timestamp" lookup is O(index seek).
const countSinceSnapshotSQL = `
SELECT COUNT(*)::BIGINT
FROM post_revisions
WHERE post_id = $1 AND created_at >= COALESCE(
    (SELECT MAX(created_at) FROM post_revisions
     WHERE post_id = $1 AND snapshot IS NOT NULL),
    '-infinity'::TIMESTAMPTZ
)
`

// latestSnapshotSQL returns the timestamp of the most recent snapshot
// for the post (used by the snapshot-age decision in Save).
const latestSnapshotSQL = `
SELECT created_at
FROM post_revisions
WHERE post_id = $1 AND snapshot IS NOT NULL
ORDER BY created_at DESC
LIMIT 1
`

// latestRevisionAnyKindSQL returns the most recent revision id for
// a post (used as DeltaFrom when Save chooses delta storage).
const latestRevisionAnyKindSQL = `
SELECT id::TEXT
FROM post_revisions
WHERE post_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1
`

// deleteByIDSQL is used by Prune to drop a single row. We issue one
// DELETE per id rather than a batch IN(...) because the per-post
// list is bounded by retention; staying parameterized keeps the
// pgx prepared-statement cache hot.
const deleteByIDSQL = `
DELETE FROM post_revisions WHERE id = $1
`

// Save inserts r into post_revisions. Returns ErrInvalidRevision
// (wrapped) on structural failures; pgx errors are wrapped with %w
// so callers can errors.Is against pgx.ErrNoRows, etc.
func (s *PostgresStore) Save(ctx context.Context, r Revision, opts ...SaveOption) (uuid.UUID, error) {
	if err := validateForSave(r); err != nil {
		return uuid.Nil, err
	}
	options := resolveSaveOptions(opts)

	if r.CreatedAt.IsZero() {
		r.CreatedAt = s.now()
	} else {
		r.CreatedAt = r.CreatedAt.UTC()
	}

	// Decide snapshot vs delta. We need three SQL probes:
	//   1. count revisions since the last snapshot (the "every Nth")
	//   2. timestamp of the last snapshot (the "every 24h")
	//   3. id of the latest revision (DeltaFrom for delta storage)
	// Force-snapshot short-circuits all three.
	useSnapshot := options.ForceSnapshot
	var deltaFromID uuid.UUID

	if !useSnapshot {
		// Probe 1: count since last snapshot.
		var sinceLastSnapshot int64
		if err := s.db.QueryRow(ctx, countSinceSnapshotSQL, r.PostID).Scan(&sinceLastSnapshot); err != nil {
			return uuid.Nil, fmt.Errorf("revisions: count since snapshot: %w", err)
		}
		// If there is no snapshot at all yet, sinceLastSnapshot
		// equals the total row count (the '-infinity' coalesce
		// matches everything). That's correct: we want the first
		// revision of a post to be a snapshot, which is what the
		// count == 0 branch below produces.
		if sinceLastSnapshot == 0 {
			useSnapshot = true
		} else if sinceLastSnapshot >= int64(options.snapshotEveryN()) {
			useSnapshot = true
		}
	}

	if !useSnapshot {
		// Probe 2: last snapshot timestamp for the age cap.
		maxAgeSec := options.maxSnapshotAgeSec()
		if maxAgeSec > 0 {
			var lastSnap time.Time
			err := s.db.QueryRow(ctx, latestSnapshotSQL, r.PostID).Scan(&lastSnap)
			switch {
			case err == nil:
				if r.CreatedAt.Sub(lastSnap) >= time.Duration(maxAgeSec)*time.Second {
					useSnapshot = true
				}
			case errors.Is(err, pgx.ErrNoRows):
				// No prior snapshot — handled above by the count
				// path; we should already have switched to snapshot.
				useSnapshot = true
			default:
				return uuid.Nil, fmt.Errorf("revisions: latest snapshot lookup: %w", err)
			}
		}
	}

	// Compute the delta payload if we're going that route. The
	// parent's materialized content drives jsondiff; we read it via
	// Materialize so we get the same chain-walk semantics as the
	// reader path.
	var deltaPayload json.RawMessage
	if !useSnapshot {
		var parentIDStr string
		if err := s.db.QueryRow(ctx, latestRevisionAnyKindSQL, r.PostID).Scan(&parentIDStr); err != nil {
			// No parent row — shouldn't happen if we made it past
			// the count probe, but fall back to snapshot to be safe.
			useSnapshot = true
		} else {
			parsed, err := uuid.Parse(parentIDStr)
			if err != nil {
				return uuid.Nil, fmt.Errorf("revisions: parse parent id: %w", err)
			}
			deltaFromID = parsed
			parentJSON, err := s.Materialize(ctx, deltaFromID)
			if err != nil {
				return uuid.Nil, fmt.Errorf("revisions: materialize parent: %w", err)
			}
			deltaPayload, err = ComputeDelta(parentJSON, r.ContentBlocks)
			if err != nil {
				return uuid.Nil, fmt.Errorf("revisions: compute delta: %w", err)
			}
		}
	}

	authorStr := ""
	if r.AuthorID != uuid.Nil {
		authorStr = r.AuthorID.String()
	}

	var (
		row pgx.Row
		idText string
	)
	if useSnapshot {
		row = s.db.QueryRow(ctx, insertSnapshotSQL,
			r.PostID,
			authorStr,
			r.CreatedAt,
			string(r.Kind),
			cloneJSON(r.ContentBlocks),
			r.Title,
			r.Excerpt,
			r.ContentBlocksHash,
			r.Comment,
		)
	} else {
		row = s.db.QueryRow(ctx, insertDeltaSQL,
			r.PostID,
			authorStr,
			r.CreatedAt,
			string(r.Kind),
			deltaFromID,
			deltaPayload,
			r.Title,
			r.Excerpt,
			r.ContentBlocksHash,
			r.Comment,
		)
	}
	if err := row.Scan(&idText); err != nil {
		return uuid.Nil, fmt.Errorf("revisions: insert: %w", err)
	}
	parsed, err := uuid.Parse(idText)
	if err != nil {
		return uuid.Nil, fmt.Errorf("revisions: parse returned id: %w", err)
	}
	return parsed, nil
}

// Get reads one revision by id.
func (s *PostgresStore) Get(ctx context.Context, id uuid.UUID) (Revision, error) {
	row := s.db.QueryRow(ctx, selectByIDSQL, id)
	r, err := scanRevision(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Revision{}, ErrNotFound
		}
		return Revision{}, fmt.Errorf("revisions: get: %w", err)
	}
	return r, nil
}

// List queries post_revisions for one post with the given filter.
// Results are sorted most-recent-first. Limit is clamped to
// postgresMaxLimit.
func (s *PostgresStore) List(ctx context.Context, postID uuid.UUID, f Filter) ([]Revision, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = postgresDefaultLimit
	}
	if limit > postgresMaxLimit {
		limit = postgresMaxLimit
	}

	var (
		startArg any = nil
		endArg   any = nil
	)
	if !f.Start.IsZero() {
		startArg = f.Start
	}
	if !f.End.IsZero() {
		endArg = f.End
	}
	authorArg := ""
	if f.AuthorID != uuid.Nil {
		authorArg = f.AuthorID.String()
	}

	rows, err := s.db.Query(ctx, listSQL,
		postID,
		startArg, endArg,
		authorArg, string(f.Kind),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("revisions: list: %w", err)
	}
	defer rows.Close()

	var out []Revision
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("revisions: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revisions: rows: %w", err)
	}
	return out, nil
}

// Latest returns the most recent revision of the given kind for postID.
func (s *PostgresStore) Latest(ctx context.Context, postID uuid.UUID, kind RevisionKind) (Revision, error) {
	if !kind.Valid() {
		return Revision{}, errors.Join(ErrInvalidRevision, fmt.Errorf("unknown Kind: %q", string(kind)))
	}
	row := s.db.QueryRow(ctx, latestByKindSQL, postID, string(kind))
	r, err := scanRevision(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Revision{}, ErrNotFound
		}
		return Revision{}, fmt.Errorf("revisions: latest: %w", err)
	}
	return r, nil
}

// Materialize reconstructs the full JSON for a revision. For a
// snapshot revision it returns Snapshot directly; for a delta it
// walks DeltaFrom back to the nearest snapshot and applies the
// patches forward.
func (s *PostgresStore) Materialize(ctx context.Context, id uuid.UUID) (json.RawMessage, error) {
	return s.materializeWithDepth(ctx, id, 0)
}

func (s *PostgresStore) materializeWithDepth(ctx context.Context, id uuid.UUID, depth int) (json.RawMessage, error) {
	if depth >= maxChainDepth {
		return nil, ErrCorruptChain
	}
	r, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Missing parent in the middle of the chain — corrupt.
			if depth > 0 {
				return nil, ErrCorruptChain
			}
			return nil, err
		}
		return nil, err
	}
	if r.isSnapshot() {
		return cloneJSON(r.Snapshot), nil
	}
	if r.DeltaFrom == uuid.Nil {
		return nil, ErrCorruptChain
	}
	parent, err := s.materializeWithDepth(ctx, r.DeltaFrom, depth+1)
	if err != nil {
		return nil, err
	}
	return ApplyDelta(parent, r.Delta)
}

// pruneListSQL pulls the (id, kind, author_id, created_at,
// is_snapshot, delta_from) tuples Prune needs to make decisions.
// We deliberately do NOT pull snapshot/delta payloads — Prune doesn't
// need the JSON, and skipping it keeps the row size small.
const pruneListSQL = `
SELECT
    id::TEXT,
    COALESCE(author_id::TEXT, ''),
    created_at,
    kind,
    (snapshot IS NOT NULL) AS is_snapshot,
    COALESCE(delta_from::TEXT, '')
FROM post_revisions
WHERE post_id = $1
ORDER BY created_at ASC, id ASC
`

// pruneCandidate is the minimal projection used by Prune.
type pruneCandidate struct {
	id         uuid.UUID
	authorID   uuid.UUID
	createdAt  time.Time
	kind       RevisionKind
	isSnapshot bool
	deltaFrom  uuid.UUID
}

// Prune applies retention to one post and returns the count of rows
// deleted. The reachability sweep is identical to MemoryStore's:
// snapshots referenced by un-pruned deltas are retained.
func (s *PostgresStore) Prune(ctx context.Context, postID uuid.UUID, retention RetentionPolicy) (int, error) {
	policy := retention.normalize()
	now := s.now()

	rows, err := s.db.Query(ctx, pruneListSQL, postID)
	if err != nil {
		return 0, fmt.Errorf("revisions: prune list: %w", err)
	}
	defer rows.Close()

	var candidates []pruneCandidate
	for rows.Next() {
		var (
			idStr, authorStr, deltaFromStr string
			c                              pruneCandidate
		)
		if err := rows.Scan(&idStr, &authorStr, &c.createdAt, &c.kind, &c.isSnapshot, &deltaFromStr); err != nil {
			return 0, fmt.Errorf("revisions: prune scan: %w", err)
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return 0, fmt.Errorf("revisions: prune parse id: %w", err)
		}
		c.id = id
		if authorStr != "" {
			a, err := uuid.Parse(authorStr)
			if err != nil {
				return 0, fmt.Errorf("revisions: prune parse author: %w", err)
			}
			c.authorID = a
		}
		if deltaFromStr != "" {
			d, err := uuid.Parse(deltaFromStr)
			if err != nil {
				return 0, fmt.Errorf("revisions: prune parse delta_from: %w", err)
			}
			c.deltaFrom = d
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("revisions: prune rows: %w", err)
	}
	rows.Close()

	if len(candidates) == 0 {
		return 0, nil
	}

	toDelete := classifyForPrune(candidates, policy, now)
	if len(toDelete) == 0 {
		return 0, nil
	}

	deleted := 0
	for _, id := range toDelete {
		tag, err := s.db.Exec(ctx, deleteByIDSQL, id)
		if err != nil {
			return deleted, fmt.Errorf("revisions: prune delete: %w", err)
		}
		deleted += int(tag.RowsAffected())
	}
	return deleted, nil
}

// classifyForPrune is the pure-function classifier shared by the
// Memory and Postgres paths. Returns the ordered set of IDs to delete.
//
// candidates MUST be in insertion order (oldest-first) — both stores
// satisfy this.
func classifyForPrune(candidates []pruneCandidate, policy RetentionPolicy, now time.Time) []uuid.UUID {
	dropMark := make(map[uuid.UUID]bool)

	// Group autosaves by author; collect manuals / publishes.
	autosavesByAuthor := make(map[uuid.UUID][]uuid.UUID)
	var manuals, publishes []uuid.UUID
	exempt := make(map[uuid.UUID]bool) // MinKeepAll exemption

	for _, c := range candidates {
		if policy.MinKeepAll > 0 && now.Sub(c.createdAt) < policy.MinKeepAll {
			exempt[c.id] = true
			continue
		}
		switch c.kind {
		case Autosave:
			autosavesByAuthor[c.authorID] = append(autosavesByAuthor[c.authorID], c.id)
		case Manual:
			manuals = append(manuals, c.id)
		case Publish:
			publishes = append(publishes, c.id)
		}
	}

	if policy.MaxAutosavesPerAuthor > 0 {
		for _, list := range autosavesByAuthor {
			markOldestForDrop(list, policy.MaxAutosavesPerAuthor, dropMark)
		}
	}
	if policy.MaxManual > 0 {
		markOldestForDrop(manuals, policy.MaxManual, dropMark)
	}
	if policy.MaxPublish > 0 {
		markOldestForDrop(publishes, policy.MaxPublish, dropMark)
	}

	if policy.MaxAgeAutosave > 0 {
		for _, c := range candidates {
			if c.kind != Autosave {
				continue
			}
			if now.Sub(c.createdAt) >= policy.MaxAgeAutosave {
				dropMark[c.id] = true
			}
		}
	}

	// Reachability sweep: keep any snapshot still referenced by an
	// un-dropped delta. We need a quick lookup of candidate by id.
	byID := make(map[uuid.UUID]pruneCandidate, len(candidates))
	for _, c := range candidates {
		byID[c.id] = c
	}
	for _, c := range candidates {
		if dropMark[c.id] {
			continue
		}
		cur := c
		for !cur.isSnapshot && cur.deltaFrom != uuid.Nil {
			if dropMark[cur.deltaFrom] {
				delete(dropMark, cur.deltaFrom)
			}
			parent, ok := byID[cur.deltaFrom]
			if !ok {
				break
			}
			cur = parent
		}
	}

	// Convert the mark-set to an ordered slice for deterministic
	// delete order. Iterate candidates so we delete oldest-first.
	out := make([]uuid.UUID, 0, len(dropMark))
	for _, c := range candidates {
		if dropMark[c.id] {
			out = append(out, c.id)
		}
	}
	return out
}

// markOldestForDrop marks the older entries of ids for deletion,
// keeping the most recent keep count. ids must be insertion-ordered
// (oldest-first).
func markOldestForDrop(ids []uuid.UUID, keep int, out map[uuid.UUID]bool) {
	if len(ids) <= keep {
		return
	}
	drop := len(ids) - keep
	for i := 0; i < drop; i++ {
		out[ids[i]] = true
	}
}

// scanRevision pulls a Revision out of either a single pgx.Row or
// pgx.Rows (both expose Scan with the same signature).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRevision(row rowScanner) (Revision, error) {
	var (
		r                                          Revision
		idStr, postIDStr, authorIDStr, deltaFromStr string
		kindStr                                    string
		delta, snapshot                            []byte
	)
	if err := row.Scan(
		&idStr, &postIDStr, &authorIDStr,
		&r.CreatedAt, &kindStr,
		&r.Title, &r.Excerpt, &r.ContentBlocksHash,
		&deltaFromStr, &delta, &snapshot, &r.Comment,
	); err != nil {
		return Revision{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		r.ID = id
	} else {
		return Revision{}, fmt.Errorf("revisions: parse id: %w", err)
	}
	if pid, err := uuid.Parse(postIDStr); err == nil {
		r.PostID = pid
	} else {
		return Revision{}, fmt.Errorf("revisions: parse post_id: %w", err)
	}
	if authorIDStr != "" {
		if aid, err := uuid.Parse(authorIDStr); err == nil {
			r.AuthorID = aid
		} else {
			return Revision{}, fmt.Errorf("revisions: parse author_id: %w", err)
		}
	}
	if deltaFromStr != "" {
		if did, err := uuid.Parse(deltaFromStr); err == nil {
			r.DeltaFrom = did
		} else {
			return Revision{}, fmt.Errorf("revisions: parse delta_from: %w", err)
		}
	}
	r.Kind = RevisionKind(kindStr)
	if len(delta) > 0 {
		r.Delta = json.RawMessage(delta)
	}
	if len(snapshot) > 0 {
		r.Snapshot = json.RawMessage(snapshot)
	}
	return r, nil
}

// Ensure PostgresStore satisfies Store at compile time.
var _ Store = (*PostgresStore)(nil)
