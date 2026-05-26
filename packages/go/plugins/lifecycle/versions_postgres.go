package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresVersionLog persists the version log against the
// plugin_version_log table (migration 000039). The methods mirror the
// VersionLog interface 1:1; transactions span multi-row mutations
// (AppendActive flips the previous active row + inserts the new one)
// so a concurrent caller cannot observe two active rows for the same
// slug.
type PostgresVersionLog struct {
	db PgxQuerier
}

// NewPostgresVersionLog wraps a pgx pool. Reuses PgxQuerier from the
// plugins-row store so test code can inject the same fake.
func NewPostgresVersionLog(db PgxQuerier) *PostgresVersionLog {
	if db == nil {
		panic("lifecycle.NewPostgresVersionLog: db is required")
	}
	return &PostgresVersionLog{db: db}
}

// AppendActive flips any current active row to retiring and inserts
// the new row as active. The two writes share a transaction so the
// "single active row per slug" invariant is preserved across
// concurrent callers.
//
// Because we share the PgxQuerier interface with PostgresStorage,
// we accept either a pool (which auto-creates a connection-scoped
// transaction inside Exec) or an explicit tx the caller threaded
// through. In the pool case we open our own BEGIN/COMMIT pair.
func (s *PostgresVersionLog) AppendActive(ctx context.Context, row VersionRow) (*VersionRow, error) {
	if row.Slug == "" || row.Version == "" {
		return nil, errors.New("lifecycle/postgres-versions: AppendActive: slug and version required")
	}

	// Read the current active row (if any) for the slug. We do this
	// inside an explicit transaction so the flip-then-insert pair is
	// atomic.
	pool, ok := s.db.(beginTxer)
	if !ok {
		// Tests pass a pre-existing transaction directly — bypass the
		// pool dance and execute the two statements in-place.
		return s.appendActiveWithin(ctx, s.db, row)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("lifecycle/postgres-versions: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	prev, err := s.appendActiveWithin(ctx, txQuerier{tx: tx}, row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("lifecycle/postgres-versions: commit: %w", err)
	}
	return prev, nil
}

// appendActiveWithin runs the flip-then-insert pair on the supplied
// querier (pool or tx). Exported only inside the package.
func (s *PostgresVersionLog) appendActiveWithin(ctx context.Context, q PgxQuerier, row VersionRow) (*VersionRow, error) {
	// Lookup the current active row first so we can return it to the
	// Manager (which uses it to know what to drain).
	var prev VersionRow
	hasPrev := false
	{
		r := q.QueryRow(ctx, `
			SELECT slug, version, abi_version, installed_at,
			       COALESCE(activated_at, 'epoch'::TIMESTAMPTZ)
			  FROM plugin_version_log
			 WHERE slug = $1 AND state = 'active'`, row.Slug)
		var activatedAt time.Time
		err := r.Scan(&prev.Slug, &prev.Version, &prev.ABIVersion, &prev.InstalledAt, &activatedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No prior active row — that's fine, first time we record
			// this slug. (The lifecycle Manager only calls AppendActive
			// from Update, which requires the slug to be Active in the
			// plugins table, but the bootstrap flow could also use it
			// to record an initial version. We don't enforce the
			// "must have a previous" rule here.)
		case err != nil:
			return nil, fmt.Errorf("lookup previous active: %w", err)
		default:
			prev.State = VersionRetiring
			if !isEpoch(activatedAt) {
				prev.ActivatedAt = activatedAt
			}
			hasPrev = true
		}
	}

	if hasPrev {
		_, err := q.Exec(ctx, `
			UPDATE plugin_version_log
			   SET state = 'retiring',
			       retired_at = $1
			 WHERE slug = $2 AND version = $3 AND state = 'active'`,
			row.InstalledAt, row.Slug, prev.Version)
		if err != nil {
			return nil, fmt.Errorf("flip previous active: %w", err)
		}
	}

	if row.ActivatedAt.IsZero() {
		row.ActivatedAt = row.InstalledAt
	}
	_, err := q.Exec(ctx, `
		INSERT INTO plugin_version_log (slug, version, abi_version, state, installed_at, activated_at)
		VALUES ($1, $2, $3, 'active', $4, $5)`,
		row.Slug, row.Version, row.ABIVersion, row.InstalledAt, row.ActivatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert new active: %w", err)
	}

	if hasPrev {
		return &prev, nil
	}
	return nil, nil
}

// MarkRetained transitions a row to retained with the given retention_end.
func (s *PostgresVersionLog) MarkRetained(ctx context.Context, slug, version string, retentionEnd time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE plugin_version_log
		   SET state = 'retained',
		       retention_end = $1
		 WHERE slug = $2 AND version = $3`,
		retentionEnd, slug, version)
	if err != nil {
		return fmt.Errorf("lifecycle/postgres-versions: MarkRetained: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("lifecycle/postgres-versions: MarkRetained %q/%q: not found", slug, version)
	}
	return nil
}

// PromoteToActive swaps a retained row to active and flips the
// previous active row to retiring in the same transaction.
func (s *PostgresVersionLog) PromoteToActive(ctx context.Context, slug, version string) error {
	pool, ok := s.db.(beginTxer)
	if !ok {
		return s.promoteWithin(ctx, s.db, slug, version)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("lifecycle/postgres-versions: PromoteToActive begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.promoteWithin(ctx, txQuerier{tx: tx}, slug, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresVersionLog) promoteWithin(ctx context.Context, q PgxQuerier, slug, version string) error {
	// The target row must exist and be retained.
	var existingState string
	if err := q.QueryRow(ctx,
		`SELECT state FROM plugin_version_log WHERE slug = $1 AND version = $2`,
		slug, version,
	).Scan(&existingState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %q/%q", ErrNoRollback, slug, version)
		}
		return fmt.Errorf("promote lookup: %w", err)
	}
	if existingState != string(VersionRetained) {
		return fmt.Errorf("%w: %q/%q not retained (state=%s)", ErrNoRollback, slug, version, existingState)
	}

	now := time.Now().UTC()
	// Flip current active → retiring.
	_, err := q.Exec(ctx, `
		UPDATE plugin_version_log
		   SET state = 'retiring', retired_at = $1
		 WHERE slug = $2 AND state = 'active'`, now, slug)
	if err != nil {
		return fmt.Errorf("promote flip active: %w", err)
	}
	// Promote target → active.
	_, err = q.Exec(ctx, `
		UPDATE plugin_version_log
		   SET state = 'active', activated_at = $1, retention_end = NULL
		 WHERE slug = $2 AND version = $3`, now, slug, version)
	if err != nil {
		return fmt.Errorf("promote target: %w", err)
	}
	return nil
}

// MarkRetired moves a row to retired (fully unloaded).
func (s *PostgresVersionLog) MarkRetired(ctx context.Context, slug, version string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE plugin_version_log SET state = 'retired' WHERE slug = $1 AND version = $2`,
		slug, version)
	if err != nil {
		return fmt.Errorf("lifecycle/postgres-versions: MarkRetired: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("lifecycle/postgres-versions: MarkRetired %q/%q: not found", slug, version)
	}
	return nil
}

// ListRetained returns retained rows for slug, newest first.
func (s *PostgresVersionLog) ListRetained(ctx context.Context, slug string) ([]VersionRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT slug, version, abi_version, installed_at,
		       COALESCE(activated_at, 'epoch'::TIMESTAMPTZ),
		       COALESCE(retired_at,    'epoch'::TIMESTAMPTZ),
		       COALESCE(retention_end, 'epoch'::TIMESTAMPTZ)
		  FROM plugin_version_log
		 WHERE slug = $1 AND state = 'retained'
		 ORDER BY installed_at DESC`, slug)
	if err != nil {
		return nil, fmt.Errorf("lifecycle/postgres-versions: ListRetained: %w", err)
	}
	defer rows.Close()
	var out []VersionRow
	for rows.Next() {
		var r VersionRow
		var actAt, retAt, retEnd time.Time
		if err := rows.Scan(&r.Slug, &r.Version, &r.ABIVersion,
			&r.InstalledAt, &actAt, &retAt, &retEnd); err != nil {
			return nil, fmt.Errorf("lifecycle/postgres-versions: ListRetained scan: %w", err)
		}
		r.State = VersionRetained
		if !isEpoch(actAt) {
			r.ActivatedAt = actAt
		}
		if !isEpoch(retAt) {
			r.RetiredAt = retAt
		}
		if !isEpoch(retEnd) {
			r.RetentionEnd = retEnd
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PurgeExpired drops retained rows whose retention_end has passed
// and any retired rows. Returns the count purged.
func (s *PostgresVersionLog) PurgeExpired(ctx context.Context, now time.Time) (int, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM plugin_version_log
		 WHERE (state = 'retained' AND retention_end < $1)
		    OR  state = 'retired'`, now)
	if err != nil {
		return 0, fmt.Errorf("lifecycle/postgres-versions: PurgeExpired: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// UpdateActiveVersion satisfies VersionedStorage on PostgresStorage:
// rewrites the version/manifest/abi on an Active plugins row without
// going through the state CAS. Reused by Update + Rollback.
func (s *PostgresStorage) UpdateActiveVersion(ctx context.Context, slug, version string, manifestBytes []byte, abiVersion int) error {
	if len(manifestBytes) == 0 {
		// Rollback path — keep the manifest column untouched.
		tag, err := s.db.Exec(ctx, `
			UPDATE plugins
			   SET version = $1,
			       abi_version = COALESCE(NULLIF($2, 0), abi_version),
			       row_version = row_version + 1,
			       updated_at = $3
			 WHERE slug = $4 AND state = 'active'`, version, abiVersion, s.now().UTC(), slug)
		if err != nil {
			return fmt.Errorf("lifecycle/postgres: UpdateActiveVersion (no manifest): %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("lifecycle/postgres: UpdateActiveVersion: row not active for %q", slug)
		}
		return nil
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE plugins
		   SET version = $1,
		       abi_version = COALESCE(NULLIF($2, 0), abi_version),
		       manifest = $3::JSONB,
		       row_version = row_version + 1,
		       updated_at = $4
		 WHERE slug = $5 AND state = 'active'`, version, abiVersion, string(manifestBytes), s.now().UTC(), slug)
	if err != nil {
		return fmt.Errorf("lifecycle/postgres: UpdateActiveVersion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("lifecycle/postgres: UpdateActiveVersion: row not active for %q", slug)
	}
	return nil
}

// beginTxer is the subset of *pgxpool.Pool we use to open an explicit
// transaction. Declared as an interface so the package doesn't need a
// direct dependency on pgxpool for this one operation; both the pool
// and the testutil fake satisfy it (or don't, in which case we fall
// through to the no-transaction branch).
type beginTxer interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// txQuerier adapts a pgx.Tx to the PgxQuerier interface so the
// statements in AppendActive / PromoteToActive can run against it
// without changing signatures.
type txQuerier struct {
	tx pgx.Tx
}

func (t txQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.tx.QueryRow(ctx, sql, args...)
}
func (t txQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}
func (t txQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}

// Compile-time check.
var _ VersionLog = (*PostgresVersionLog)(nil)
