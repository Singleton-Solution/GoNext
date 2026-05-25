package posts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxAutosaveStore is the production AutosaveStore backed by Postgres
// via pgx. It writes to the post_autosaves table introduced in
// migration 000016 and respects the same lock-holder gate that the
// in-memory store mimics: if another user holds an unexpired lock on
// the post (post_locks row from migration 000010), Put refuses with
// ErrLocked rather than clobber the in-flight editor.
//
// The pool is borrowed (not owned); the caller is responsible for
// closing it at shutdown. Construct via NewPgxAutosaveStore.
//
// All methods are safe for concurrent use — every call runs through
// the pool, which manages its own connection pool semantics, and each
// Put runs inside a single transaction so the lock-check + upsert are
// atomic against any concurrent Put for the same post.
type PgxAutosaveStore struct {
	pool *pgxpool.Pool
}

// NewPgxAutosaveStore wraps a *pgxpool.Pool in the production
// AutosaveStore. The pool is borrowed (not owned).
func NewPgxAutosaveStore(pool *pgxpool.Pool) *PgxAutosaveStore {
	return &PgxAutosaveStore{pool: pool}
}

// Compile-time check that PgxAutosaveStore implements AutosaveStore.
var _ AutosaveStore = (*PgxAutosaveStore)(nil)

// selectAutosaveSQL reads the latest autosave for (post_id, user_id).
// The PK on (post_id, user_id) makes this an index-only lookup; no
// LIMIT / ORDER BY needed because the schema enforces "one row per
// (post, author)".
const selectAutosaveSQL = `
SELECT blocks, updated_at
  FROM post_autosaves
 WHERE post_id = $1 AND user_id = $2
`

// upsertAutosaveSQL writes (or refreshes) the autosave row. ON CONFLICT
// updates blocks + updated_at in place; the (post_id, user_id) PK makes
// "latest wins" the only coherent outcome. RETURNING gives us the
// post-update updated_at so callers don't need a follow-up SELECT.
//
// updated_at is set to now() (server-side clock) rather than letting the
// schema default fire; defaults only apply on INSERT, and we need the
// timestamp refreshed on UPDATE too. now() reads the transaction-start
// timestamp, which is the right semantic for "this autosave was
// committed at T".
const upsertAutosaveSQL = `
INSERT INTO post_autosaves (post_id, user_id, blocks, updated_at)
VALUES ($1, $2, $3::jsonb, now())
ON CONFLICT (post_id, user_id) DO UPDATE
   SET blocks     = EXCLUDED.blocks,
       updated_at = now()
RETURNING updated_at
`

// selectLockHolderSQL checks the post_locks table for a current,
// unexpired holder of the post lock. Returns the holder's user_id when
// somebody (other than the caller) holds the lock, or no row when the
// lock is free or expired. We do the check inside the same transaction
// as the upsert so a concurrent acquire_post_lock() can't slip in
// between the check and the write.
//
// The expires_at >= now() filter keeps stale rows (heartbeat died,
// session expired) from blocking the autosave — matches the same
// semantics acquire_post_lock() applies in migration 000010.
const selectLockHolderSQL = `
SELECT user_id
  FROM post_locks
 WHERE post_id = $1
   AND expires_at >= now()
 FOR UPDATE
`

// Get returns the latest autosave for (postID, userID). ErrNotFound
// when the user has no in-flight draft for this post.
//
// We don't take a transaction here — the read is a single index lookup
// on the PK and the result is immediately stale from the caller's
// perspective (somebody else could write another autosave a moment
// later). A non-transactional read keeps the connection-pool pressure
// low; the writer-side guarantees (PK + ON CONFLICT) already ensure
// the row we read is a coherent snapshot.
func (s *PgxAutosaveStore) Get(ctx context.Context, postID, userID string) (Autosave, error) {
	var (
		blocks    []byte
		updatedAt time.Time
	)
	err := s.pool.QueryRow(ctx, selectAutosaveSQL, postID, userID).Scan(&blocks, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Autosave{}, ErrNotFound
		}
		return Autosave{}, fmt.Errorf("posts.PgxAutosaveStore.Get: %w", err)
	}
	return Autosave{
		PostID:    postID,
		UserID:    userID,
		Blocks:    json.RawMessage(blocks),
		UpdatedAt: updatedAt.UTC(),
	}, nil
}

// Put upserts the autosave row for (postID, userID). If another user
// holds an unexpired lock on the post, Put returns ErrLocked without
// writing. Same-user re-acquires fall through to the upsert (the
// common case: the editor heartbeats every 60s, autosaves every 30s).
//
// The lock check and the upsert run inside a single transaction with
// the lock row read FOR UPDATE, so a concurrent acquire_post_lock()
// from another session blocks until we commit. This matches the
// atomicity guarantee the MemoryAutosaveStore provides via s.mu.
func (s *PgxAutosaveStore) Put(ctx context.Context, postID, userID string, blocks json.RawMessage) (Autosave, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Autosave{}, fmt.Errorf("posts.PgxAutosaveStore.Put: begin: %w", err)
	}
	// Defer Rollback unconditionally; pgx treats Rollback after Commit
	// as a no-op (returns pgx.ErrTxClosed which we ignore), so this is
	// the idiomatic safety net.
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock-holder gate. If somebody else holds the lock, refuse the
	// write — the editor surfaces this as 423 Locked. Same-user holds
	// fall through (the editor heartbeating its own lock should not
	// block its own autosave). An empty result (no row, or expired) is
	// "the lock is free", also fall through.
	var holder string
	err = tx.QueryRow(ctx, selectLockHolderSQL, postID).Scan(&holder)
	switch {
	case err == nil:
		if holder != "" && holder != userID {
			return Autosave{}, ErrLocked
		}
	case errors.Is(err, pgx.ErrNoRows):
		// Lock free or expired; fall through to the upsert.
	default:
		return Autosave{}, fmt.Errorf("posts.PgxAutosaveStore.Put: lock check: %w", err)
	}

	// Normalize an empty blocks payload to a JSON null so the JSONB
	// column never sees an empty byte string (which pgx would reject
	// as invalid JSON). The validator at the handler layer already
	// rejects an empty body, but the store should not depend on that.
	payload := blocks
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}

	var updatedAt time.Time
	if err := tx.QueryRow(ctx, upsertAutosaveSQL, postID, userID, []byte(payload)).Scan(&updatedAt); err != nil {
		return Autosave{}, fmt.Errorf("posts.PgxAutosaveStore.Put: upsert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Autosave{}, fmt.Errorf("posts.PgxAutosaveStore.Put: commit: %w", err)
	}

	// Return a fresh json.RawMessage so the caller can't mutate our
	// input buffer (the memory store does the same defensive copy).
	out := make(json.RawMessage, len(payload))
	copy(out, payload)
	return Autosave{
		PostID:    postID,
		UserID:    userID,
		Blocks:    out,
		UpdatedAt: updatedAt.UTC(),
	}, nil
}

// sweepSQL deletes every row whose updated_at is older than the
// supplied threshold. The post_autosaves_updated_at_idx (btree on
// updated_at, created in migration 000016) lets the planner do a range
// scan rather than a sequential scan.
//
// Idempotent: running it twice in a row drops the same set of rows the
// second time around (empty). Used by the daily cron in
// packages/go/jobs/cron via main.go wiring.
const sweepSQL = `
DELETE FROM post_autosaves WHERE updated_at < $1
`

// Sweep deletes every post_autosaves row whose updated_at is older
// than olderThan. Returns the number of rows deleted so the caller
// (the cron job) can log it. The contract matches the TTL described
// in migration 000016: in production the threshold is now() - 7 days,
// invoked daily.
//
// Sweep is NOT part of the AutosaveStore interface — the handler path
// never invokes it. It lives on PgxAutosaveStore because the cron
// wiring needs concrete pgx access; the in-memory store has no TTL
// semantic to mirror.
func (s *PgxAutosaveStore) Sweep(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, sweepSQL, olderThan)
	if err != nil {
		return 0, fmt.Errorf("posts.PgxAutosaveStore.Sweep: %w", err)
	}
	return tag.RowsAffected(), nil
}
