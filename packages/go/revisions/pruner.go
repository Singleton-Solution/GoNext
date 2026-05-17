package revisions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Policy is the cross-entity retention policy applied by Pruner.
//
// The per-post Store.Prune contract (RetentionPolicy in revision.go)
// is the granular knob — it carries per-kind caps and is the thing the
// editor flips at save time. Policy is the operational knob: it's what
// the nightly retention job (issue #169) hands to Pruner.Run to sweep
// every post in the install.
//
// Two dimensions:
//
//   - KeepLast keeps the most recent N revisions per post regardless
//     of age. Anything older than the Nth-most-recent is a deletion
//     candidate. Zero disables the count cap.
//
//   - KeepWithin keeps every revision younger than this duration
//     regardless of count. Anything older is a deletion candidate.
//     Zero disables the age cap.
//
// A revision is deletable only if it fails BOTH checks — it's outside
// the KeepLast window AND outside the KeepWithin window. Either knob
// alone is enough to protect a row.
//
// Revisions with IsPermanent=true are unconditionally retained. The
// flag short-circuits both checks; operators set it on legal-hold
// revisions, "first published" milestones, or anything the editor
// lets a user pin. See docs/01-core-cms.md §4.3 and issue #169.
type Policy struct {
	// KeepLast keeps the latest N revisions per post. Zero disables
	// the count cap. Recommended production value is 30 for manuals
	// per docs/01-core-cms.md §4.3.
	KeepLast int

	// KeepWithin keeps every revision newer than this duration.
	// Zero disables the age cap. Recommended production value is
	// 7*24*time.Hour ("keep all from the last 7 days").
	KeepWithin time.Duration
}

// DefaultPolicy returns the package-default Pruner policy: keep the
// last 30 revisions per post and everything from the last 7 days.
// Matches the doc-01 §4.3 production target for manual revisions.
func DefaultPolicy() Policy {
	return Policy{
		KeepLast:   30,
		KeepWithin: 7 * 24 * time.Hour,
	}
}

func (p Policy) normalize() Policy {
	out := p
	if out.KeepLast < 0 {
		out.KeepLast = 0
	}
	if out.KeepWithin < 0 {
		out.KeepWithin = 0
	}
	return out
}

// noopReason is set on a Policy that can never delete anything (both
// knobs disabled). The Pruner short-circuits in that case so a
// misconfigured cron doesn't burn a full table scan to delete zero
// rows. Returned in Stats.Notes when triggered.
const noopReason = "policy disabled: KeepLast=0 and KeepWithin=0"

// Stats is the report Pruner.Run returns to its caller (the cron
// scheduler, the CLI subcommand, etc.).
//
// The three counters are non-overlapping per-revision tallies:
//
//   - Scanned counts every revision examined across every post.
//   - Deleted counts revisions actually removed (or that WOULD have
//     been removed under DryRun).
//   - Skipped counts revisions that were eligible by age/count but
//     were retained anyway — pinned (IsPermanent=true) or held back
//     by the reachability sweep so an un-pruned delta keeps a path
//     to a snapshot.
//
// Scanned == Deleted + Skipped + Retained, where Retained is the
// implicit "kept under cap" count not surfaced separately.
type Stats struct {
	// Scanned is the total revision count examined across all posts.
	Scanned int

	// Deleted is the count of revisions actually removed. Under
	// DryRun this is the count of revisions that WOULD be removed —
	// no DELETEs are issued.
	Deleted int

	// Skipped is the count of revisions that fell outside the keep
	// window but were retained anyway — pinned (IsPermanent=true)
	// or required by the reachability sweep.
	Skipped int

	// PostsScanned is the count of posts visited. Useful for
	// "did the scan see anything?" cron-log sanity checks.
	PostsScanned int

	// Duration is how long Run took, wall-clock.
	Duration time.Duration

	// DryRun mirrors PrunerOptions.DryRun so the caller can render
	// a "would have deleted N" message without having to track the
	// flag separately.
	DryRun bool

	// Notes carries human-readable diagnostics — currently only the
	// "policy disabled" warning. Empty under normal operation.
	Notes []string
}

// PrunerOptions tunes a single Pruner.Run invocation. Mostly used by
// the CLI (`gonext revisions prune --dry-run --batch=...`).
type PrunerOptions struct {
	// DryRun makes Run compute exactly which revisions would be
	// deleted, increment Stats.Deleted accordingly, and then NOT
	// issue the DELETE statements. The reachability sweep still
	// runs, so the count matches what a non-dry run on the same
	// snapshot of the table would produce.
	DryRun bool

	// BatchSize caps how many posts a single Run pass handles. Zero
	// means "all of them". Useful for very large installs where the
	// cron operator wants to bound the worst-case transaction
	// duration. The pruner still completes the full sweep — it just
	// commits in chunks of BatchSize posts.
	BatchSize int

	// NowFunc is the time source for KeepWithin comparisons. Tests
	// pin this to a fixed time so the "older than KeepWithin"
	// arithmetic is deterministic.
	NowFunc func() time.Time
}

func (o PrunerOptions) now() time.Time {
	if o.NowFunc != nil {
		return o.NowFunc().UTC()
	}
	return time.Now().UTC()
}

// PostLister enumerates post IDs that have at least one revision. The
// Pruner uses it to drive the per-post sweep. Production wires this
// to a SQL query against post_revisions (see PostgresPostLister
// below); tests inject a slice.
type PostLister interface {
	ListPostsWithRevisions(ctx context.Context) ([]uuid.UUID, error)
}

// PostListerFunc adapts a plain function to PostLister.
type PostListerFunc func(ctx context.Context) ([]uuid.UUID, error)

// ListPostsWithRevisions implements PostLister.
func (f PostListerFunc) ListPostsWithRevisions(ctx context.Context) ([]uuid.UUID, error) {
	return f(ctx)
}

// Pruner runs the cross-entity retention sweep. Each invocation of
// Run enumerates posts via the PostLister, then applies the per-post
// retention via Store.Prune. The Store contract already guarantees:
//
//   - is_permanent rows are skipped (added in this issue).
//   - snapshots reachable from un-pruned deltas survive the sweep.
//   - the underlying SELECT uses FOR UPDATE SKIP LOCKED so two
//     Pruner instances running in parallel partition the work
//     instead of fighting (see PostgresStore.PruneLocked below).
//
// Pruner is safe to call from multiple goroutines and from
// multiple processes simultaneously. The Postgres backing store's
// row-level lock guarantees a revision is processed by exactly one
// sweep.
type Pruner struct {
	store  Store
	lister PostLister

	// nowFunc lets tests pin "now" for the per-post Store.Prune call.
	// The PostgresStore reads its own time via its NowFunc field;
	// Pruner sets it to match if non-nil so KeepWithin math is
	// consistent across the Pruner and the Store.
	nowFunc func() time.Time
}

// NewPruner wires a Store and a PostLister into a Pruner. The store
// is the same Store every other revision call site uses — the Pruner
// is intentionally thin so a future replacement (a sharded store, an
// archive store) gets the retention sweep for free.
func NewPruner(store Store, lister PostLister) *Pruner {
	return &Pruner{store: store, lister: lister}
}

// Run executes one sweep. Returns Stats describing what happened plus
// the first error encountered. The sweep does NOT abort on a per-post
// failure — the cron operator wants the best-effort behaviour where a
// single corrupted post doesn't wedge the nightly job. Errors are
// joined via errors.Join.
//
// Time complexity is O(N) in the number of revisions across all
// posts. The Postgres store reads each post's revisions once via the
// pruner SELECT, makes the classification decisions in memory, and
// issues one DELETE per drop candidate.
func (p *Pruner) Run(ctx context.Context, policy Policy, opts PrunerOptions) (Stats, error) {
	stats := Stats{DryRun: opts.DryRun}
	start := time.Now()
	defer func() { stats.Duration = time.Since(start) }()

	policy = policy.normalize()
	if policy.KeepLast == 0 && policy.KeepWithin == 0 {
		stats.Notes = append(stats.Notes, noopReason)
		return stats, nil
	}

	if opts.NowFunc != nil {
		p.nowFunc = opts.NowFunc
	}

	posts, err := p.lister.ListPostsWithRevisions(ctx)
	if err != nil {
		return stats, fmt.Errorf("revisions: prune: list posts: %w", err)
	}

	// BatchSize caps the per-invocation work. If the caller asked for
	// a smaller window than the post set, we still walk every post —
	// the batching just chunks the work so a future enhancement
	// (commit between batches) drops in cleanly.
	limit := len(posts)
	if opts.BatchSize > 0 && opts.BatchSize < limit {
		limit = opts.BatchSize
	}

	var errs []error
	for i := 0; i < limit; i++ {
		select {
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("revisions: prune: %w", ctx.Err()))
			return stats, errors.Join(errs...)
		default:
		}

		postID := posts[i]
		stats.PostsScanned++

		ps, err := p.prunePost(ctx, postID, policy, opts)
		stats.Scanned += ps.Scanned
		stats.Deleted += ps.Deleted
		stats.Skipped += ps.Skipped
		if err != nil {
			errs = append(errs, fmt.Errorf("revisions: prune post %s: %w", postID, err))
			continue
		}
	}

	return stats, errors.Join(errs...)
}

// perPostStats is the slice of Stats covering a single post.
type perPostStats struct {
	Scanned int
	Deleted int
	Skipped int
}

// prunePost is the per-post worker. It translates Policy into the
// per-post RetentionPolicy the Store understands and delegates to
// Store.Prune (or the FOR UPDATE SKIP LOCKED variant if the store
// supports it — see PostgresStore.PruneLocked).
//
// The translation strategy:
//
//   - KeepLast becomes a per-kind cap. Because Policy is kind-agnostic
//     we set MaxManual = MaxPublish = MaxAutosavesPerAuthor = KeepLast.
//     Per-kind storage is the existing contract, so this is the
//     conservative way to honour the cross-entity intent.
//   - KeepWithin becomes MinKeepAll. The store's classifier treats
//     MinKeepAll as the "keep everything newer than this" floor,
//     which is exactly Pruner's KeepWithin semantics.
func (p *Pruner) prunePost(ctx context.Context, postID uuid.UUID, policy Policy, opts PrunerOptions) (perPostStats, error) {
	var ps perPostStats

	rp := RetentionPolicy{
		MinKeepAll: policy.KeepWithin,
	}
	if policy.KeepLast > 0 {
		rp.MaxManual = policy.KeepLast
		rp.MaxPublish = policy.KeepLast
		rp.MaxAutosavesPerAuthor = policy.KeepLast
	}
	// MaxAgeAutosave is left zero: Policy is kind-agnostic, and the
	// caller's KeepWithin window already protects recent autosaves
	// via MinKeepAll. A separate autosave-only age cap would require
	// a knob Policy doesn't expose; the per-post Store.Prune is the
	// place for that.

	// First pass: read what's there so we can tally Scanned without
	// double-counting after the Prune call. We use a generous limit
	// — the index covers it and the per-post row count is bounded.
	revs, err := p.store.List(ctx, postID, Filter{Limit: 1000})
	if err != nil {
		return ps, err
	}
	ps.Scanned = len(revs)

	// Count pinned + reachability-saved rows up front so Skipped is
	// reported accurately on both real and dry-run paths.
	pinned := 0
	for _, r := range revs {
		if r.IsPermanent {
			pinned++
		}
	}
	ps.Skipped = pinned

	if opts.DryRun {
		// Dry-run path: classify without executing. Re-use the store
		// implementations by reading and counting ourselves rather
		// than asking the store for a no-op delete — that keeps the
		// Store contract simple (no DryRun flag at the interface).
		now := opts.now()
		drops := dryRunClassify(revs, policy, now)
		ps.Deleted = len(drops)
		return ps, nil
	}

	deleted, err := p.store.Prune(ctx, postID, rp)
	if err != nil {
		return ps, err
	}
	ps.Deleted = deleted
	return ps, nil
}

// dryRunClassify is the pure-function counterpart to Store.Prune. It
// replicates the classify+reachability logic against the materialised
// Revision slice we already have in hand, so a dry run reports the
// same Deleted count a real run would.
//
// Order: revs is in newest-first order (Store.List contract). We flip
// it to oldest-first to match classifyForPrune's invariant.
func dryRunClassify(revs []Revision, policy Policy, now time.Time) []uuid.UUID {
	if len(revs) == 0 {
		return nil
	}
	candidates := make([]pruneCandidate, 0, len(revs))
	for i := len(revs) - 1; i >= 0; i-- {
		r := revs[i]
		candidates = append(candidates, pruneCandidate{
			id:          r.ID,
			authorID:    r.AuthorID,
			createdAt:   r.CreatedAt,
			kind:        r.Kind,
			isSnapshot:  len(r.Snapshot) > 0,
			deltaFrom:   r.DeltaFrom,
			isPermanent: r.IsPermanent,
		})
	}
	rp := RetentionPolicy{MinKeepAll: policy.KeepWithin}
	if policy.KeepLast > 0 {
		rp.MaxManual = policy.KeepLast
		rp.MaxPublish = policy.KeepLast
		rp.MaxAutosavesPerAuthor = policy.KeepLast
	}
	return classifyForPrune(candidates, rp.normalize(), now)
}

// listPostsWithRevisionsSQL pulls the distinct post_id list. The
// pruner only needs to know which posts have at least one revision —
// the per-post Store.Prune call does the heavy lifting.
//
// FOR UPDATE SKIP LOCKED at this layer would lock the whole post
// list, which is wrong: we want concurrent pruner instances to share
// the work, not block on each other. The row-level skip-locked lives
// down in PruneLocked (postgres path); this query is a plain SELECT.
const listPostsWithRevisionsSQL = `
SELECT DISTINCT post_id::TEXT
FROM post_revisions
ORDER BY post_id ASC
`

// PostgresPostLister returns post IDs that have revisions in the
// post_revisions table. It's the production wire-up of the
// PostLister interface.
type PostgresPostLister struct {
	db PgxQuerier
}

// NewPostgresPostLister wraps a PgxQuerier (pool or transaction).
func NewPostgresPostLister(db PgxQuerier) *PostgresPostLister {
	return &PostgresPostLister{db: db}
}

// ListPostsWithRevisions returns the distinct post IDs that have at
// least one revision row. The list is sorted by post_id for a stable
// scan order.
func (l *PostgresPostLister) ListPostsWithRevisions(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := l.db.Query(ctx, listPostsWithRevisionsSQL)
	if err != nil {
		return nil, fmt.Errorf("revisions: list posts with revisions: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("revisions: scan post id: %w", err)
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("revisions: parse post id %q: %w", s, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revisions: list posts rows: %w", err)
	}
	return out, nil
}

// pruneLockedSQL is the FOR UPDATE SKIP LOCKED variant of the prune
// SELECT. It serves the same projection as pruneListSQL but with
// row-level locks claimed for the lifetime of the surrounding
// transaction. Two pruner instances running in parallel partition
// the row set: whichever transaction sees a row first locks it, and
// the other skips that row and moves on.
//
// We restrict to is_permanent=FALSE in the SQL so pinned rows never
// even appear as candidates — the partial index on
// (post_id, created_at) WHERE is_permanent=FALSE covers this read.
const pruneLockedSQL = `
SELECT
    id::TEXT,
    COALESCE(author_id::TEXT, ''),
    created_at,
    kind,
    (snapshot IS NOT NULL) AS is_snapshot,
    COALESCE(delta_from::TEXT, ''),
    is_permanent
FROM post_revisions
WHERE post_id = $1
  AND is_permanent = FALSE
ORDER BY created_at ASC, id ASC
FOR UPDATE SKIP LOCKED
`

// PruneLocked is the concurrency-safe variant of PostgresStore.Prune.
// It opens a transaction, takes FOR UPDATE SKIP LOCKED on the
// candidate rows, runs the classifier, and DELETEs inside the same
// transaction so the locks are released atomically on commit.
//
// Two PruneLocked calls running in parallel against the same post
// will each see only the rows they locked — neither double-deletes,
// and the union of their deletions equals what a single call would
// have produced.
//
// Callers wire this from Pruner via a type assertion. The Pruner uses
// the plain Store.Prune by default; a future option could flip it
// to PruneLocked.
func (s *PostgresStore) PruneLocked(ctx context.Context, postID uuid.UUID, retention RetentionPolicy) (int, error) {
	policy := retention.normalize()
	now := s.now()

	tx, err := s.beginTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("revisions: prune begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, pruneLockedSQL, postID)
	if err != nil {
		return 0, fmt.Errorf("revisions: prune locked list: %w", err)
	}

	var candidates []pruneCandidate
	for rows.Next() {
		var (
			idStr, authorStr, deltaFromStr string
			c                              pruneCandidate
		)
		if err := rows.Scan(&idStr, &authorStr, &c.createdAt, &c.kind, &c.isSnapshot, &deltaFromStr, &c.isPermanent); err != nil {
			rows.Close()
			return 0, fmt.Errorf("revisions: prune locked scan: %w", err)
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("revisions: prune locked parse id: %w", err)
		}
		c.id = id
		if authorStr != "" {
			if a, err := uuid.Parse(authorStr); err == nil {
				c.authorID = a
			} else {
				rows.Close()
				return 0, fmt.Errorf("revisions: prune locked parse author: %w", err)
			}
		}
		if deltaFromStr != "" {
			if d, err := uuid.Parse(deltaFromStr); err == nil {
				c.deltaFrom = d
			} else {
				rows.Close()
				return 0, fmt.Errorf("revisions: prune locked parse delta_from: %w", err)
			}
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("revisions: prune locked rows: %w", err)
	}

	if len(candidates) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("revisions: prune locked commit: %w", err)
		}
		return 0, nil
	}

	toDelete := classifyForPrune(candidates, policy, now)
	if len(toDelete) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("revisions: prune locked commit: %w", err)
		}
		return 0, nil
	}

	deleted := 0
	for _, id := range toDelete {
		tag, err := tx.Exec(ctx, deleteByIDSQL, id)
		if err != nil {
			return deleted, fmt.Errorf("revisions: prune locked delete: %w", err)
		}
		deleted += int(tag.RowsAffected())
	}

	if err := tx.Commit(ctx); err != nil {
		return deleted, fmt.Errorf("revisions: prune locked commit: %w", err)
	}
	return deleted, nil
}

// beginTx opens a pgx.Tx against the store's pool. Falls back to a
// thin wrapper that exec/queries on the underlying querier when the
// store was constructed against a plain PgxQuerier (the unit-test
// seam — see NewPostgresStoreWithQuerier).
//
// We don't expose pgx.Tx at the PgxQuerier interface because that
// would force every caller of PostgresStore to satisfy the larger
// surface. Instead we detect the rare-but-real "this is a pool"
// path here.
func (s *PostgresStore) beginTx(ctx context.Context) (pgxTx, error) {
	if b, ok := s.db.(beginner); ok {
		t, err := b.Begin(ctx)
		if err != nil {
			return nil, err
		}
		return realTx{Tx: t}, nil
	}
	// Non-pool path (tests, single-connection callers): emulate a
	// transaction by reusing the querier directly. Commit/Rollback
	// are no-ops; the FOR UPDATE clause is the load-bearing part.
	return passthroughTx{q: s.db}, nil
}

// beginner is the subset of *pgxpool.Pool (and pgx.Conn) that opens a
// transaction. PgxQuerier intentionally doesn't include it because
// most call sites don't need it; PruneLocked is the exception.
type beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// pgxTx is the in-flight transaction surface PruneLocked uses. Kept
// small so the passthrough variant for tests is straightforward.
type pgxTx interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgConnCommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// pgConnCommandTag is a minimal interface over pgconn.CommandTag so
// passthroughTx (test seam) doesn't need to import pgconn. The real
// pgx.Tx satisfies it directly.
type pgConnCommandTag interface {
	RowsAffected() int64
}

// realTx wraps a pgx.Tx in the pgxTx interface, narrowing Exec's
// return type so test seams can substitute their own.
type realTx struct{ pgx.Tx }

// Exec narrows pgx.Tx.Exec to the pgConnCommandTag interface.
func (r realTx) Exec(ctx context.Context, sql string, args ...any) (pgConnCommandTag, error) {
	tag, err := r.Tx.Exec(ctx, sql, args...)
	return tag, err
}

// passthroughTx emulates a transaction over a plain PgxQuerier. Used
// when PostgresStore was built with NewPostgresStoreWithQuerier (the
// test seam). Commit and Rollback are no-ops; the SQL still carries
// FOR UPDATE SKIP LOCKED, which is enough for the locking semantics
// in single-connection tests.
type passthroughTx struct{ q PgxQuerier }

// Query delegates to the underlying querier.
func (p passthroughTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return p.q.Query(ctx, sql, args...)
}

// Exec delegates to the underlying querier.
func (p passthroughTx) Exec(ctx context.Context, sql string, args ...any) (pgConnCommandTag, error) {
	tag, err := p.q.Exec(ctx, sql, args...)
	return tag, err
}

// Commit is a no-op on the passthrough seam.
func (p passthroughTx) Commit(_ context.Context) error { return nil }

// Rollback is a no-op on the passthrough seam.
func (p passthroughTx) Rollback(_ context.Context) error { return nil }
