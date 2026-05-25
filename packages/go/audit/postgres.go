package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresDefaultLimit is the default cap when Filter.Limit is zero.
const postgresDefaultLimit = 100

// postgresMaxLimit is the hard upper bound the Postgres store imposes
// on List, regardless of what the caller asked for. The admin UI is
// expected to paginate; this guard exists for the case where a buggy
// or curious caller asks for a million rows.
const postgresMaxLimit = 1000

// PgxQuerier is the subset of *pgxpool.Pool used by PostgresStore.
//
// Exposing the interface (rather than taking *pgxpool.Pool directly)
// keeps PostgresStore testable with a pgxmock-style fake and lets
// callers swap in a tx (pgx.Tx implements the same methods) when they
// need to emit an audit event as part of a larger transaction.
//
// Exec is included because Sweep issues a DELETE; both *pgxpool.Pool
// and pgx.Tx satisfy this shape.
type PgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PostgresStore writes audit rows via INSERT to the audit_log table
// documented in docs/06-auth-permissions.md §13.
//
// The actual CREATE TABLE migration lives in a downstream issue — this
// store exists to lock the column contract so that when the migration
// lands, the Go side already speaks SQL against it. INSERTs on a host
// where audit_log does not yet exist will fail at the database with
// the usual pgx UndefinedTable error; callers should treat audit-emit
// failures as non-fatal in the boot path until the table exists.
type PostgresStore struct {
	db PgxQuerier

	// NowFunc, if set, replaces time.Now for Event.Time defaults. Used
	// only when the caller leaves Event.Time zero; supplied times are
	// passed straight through.
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
		return s.NowFunc()
	}
	return time.Now()
}

// insertSQL is the canonical INSERT used to lock the audit_log contract.
//
// Column list intentionally matches docs/06-auth-permissions.md §13.
// The schema migration that creates the table will use these names.
const insertSQL = `
INSERT INTO audit_log (
    occurred_at,
    actor_user_id,
    actor_kind,
    actor_label,
    event,
    target_kind,
    target_id,
    ip,
    user_agent,
    metadata,
    severity,
    prev_hash
) VALUES (
    $1, NULLIF($2, '')::UUID, $3, NULLIF($4, ''), $5,
    NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, '')::INET, $9,
    $10, $11, $12
) RETURNING id::TEXT
`

// listSQL selects events with optional filters folded in via NULL
// passthrough. Using NULL-or-equal for each predicate keeps the
// statement plan stable across filter combinations (no string
// concatenation, no risk of injection).
const listSQL = `
SELECT
    id::TEXT,
    occurred_at,
    COALESCE(actor_user_id::TEXT, ''),
    COALESCE(actor_label, ''),
    event,
    COALESCE(target_kind, ''),
    COALESCE(target_id, ''),
    COALESCE(ip::TEXT, ''),
    COALESCE(user_agent, ''),
    metadata,
    severity,
    prev_hash
FROM audit_log
WHERE ($1::TIMESTAMPTZ IS NULL OR occurred_at >= $1)
  AND ($2::TIMESTAMPTZ IS NULL OR occurred_at <= $2)
  AND ($3 = '' OR actor_user_id = $3::UUID)
  AND ($4 = '' OR actor_label = $4)
  AND ($5 = '' OR event = $5)
  AND ($6 = '' OR severity = $6)
ORDER BY occurred_at DESC, id DESC
LIMIT $7
`

// Emit inserts e into audit_log. Returns ErrInvalidEvent (wrapped) if
// e fails structural validation; any other error is the underlying
// pgx error wrapped with %w so callers can errors.Is against
// pgx.ErrNoRows, context.DeadlineExceeded, etc.
func (s *PostgresStore) Emit(ctx context.Context, e Event) error {
	if err := validateForEmit(e); err != nil {
		return err
	}
	normalized := e.normalize(s.now)

	actorKind := "user"
	if normalized.ActorPluginSlug != "" {
		actorKind = "plugin"
	} else if normalized.ActorUserID == "" {
		// Pre-auth events (failed login) and system actions both land
		// here. Distinguishing them requires application context the
		// store doesn't have; callers who want "system" explicitly can
		// set Metadata["actor_kind"] = "system" and we'll honor it.
		// The override is enum-checked against the allowed set so a
		// plugin author can't write arbitrary strings into the column.
		if v, ok := normalized.Metadata["actor_kind"].(string); ok && v != "" {
			if !isValidActorKind(v) {
				return errors.Join(ErrInvalidEvent, fmt.Errorf("audit: invalid actor_kind override %q (must be one of user, plugin, system)", v))
			}
			actorKind = v
		} else {
			actorKind = "system"
		}
	}

	metaJSON, err := json.Marshal(normalized.Metadata)
	if err != nil {
		return fmt.Errorf("audit: marshal metadata: %w", err)
	}
	if normalized.Metadata == nil {
		// json.Marshal(nil) emits "null"; the column default is '{}'.
		// Send "{}" explicitly to keep the row shape stable.
		metaJSON = []byte("{}")
	}

	row := s.db.QueryRow(ctx, insertSQL,
		normalized.Time,
		normalized.ActorUserID,
		actorKind,
		normalized.ActorPluginSlug,
		normalized.EventType,
		normalized.ResourceType,
		normalized.ResourceID,
		normalized.IP,
		normalized.UserAgent,
		metaJSON,
		string(normalized.Severity),
		normalized.PrevHash,
	)
	var id string
	if err := row.Scan(&id); err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	return nil
}

// List queries audit_log with the given filter applied. Results are
// sorted most-recent-first. The limit is clamped to postgresMaxLimit.
func (s *PostgresStore) List(ctx context.Context, f Filter) ([]Event, error) {
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

	rows, err := s.db.Query(ctx, listSQL,
		startArg, endArg,
		f.ActorUserID, f.PluginSlug, f.EventType,
		string(f.Severity), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var (
			e        Event
			metaJSON []byte
			sev      string
		)
		if err := rows.Scan(
			&e.ID, &e.Time,
			&e.ActorUserID, &e.ActorPluginSlug,
			&e.EventType,
			&e.ResourceType, &e.ResourceID,
			&e.IP, &e.UserAgent,
			&metaJSON, &sev, &e.PrevHash,
		); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		if len(metaJSON) > 0 && !bytesEqual(metaJSON, []byte("null")) {
			if err := json.Unmarshal(metaJSON, &e.Metadata); err != nil {
				return nil, fmt.Errorf("audit: unmarshal metadata: %w", err)
			}
		}
		e.Severity = Severity(sev)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: rows: %w", err)
	}
	return out, nil
}

// sweepSQL is the retention-prune statement. We delete in a single
// bounded statement keyed on occurred_at and severity='info'; the
// partial index audit_occurred_sweep_idx (see migration 000029) is
// shaped for exactly this predicate. Critical/warning rows are kept
// indefinitely per docs/06-auth-permissions.md §13.2.
//
// We return the deleted-row count so the cron caller can log it and
// alert on a sweep that's mysteriously deleting orders-of-magnitude
// more rows than expected (the canonical "are we leaking events?"
// signal).
const sweepSQL = `
DELETE FROM audit_log
WHERE occurred_at < $1
  AND severity = 'info'
`

// Sweep deletes 'info'-severity audit rows older than the retention
// horizon. Rows with severity='warning' or 'critical' are retained
// indefinitely (docs/06-auth-permissions.md §13.2 — operators purge
// them manually under a documented compliance procedure).
//
// retention is a duration; rows whose occurred_at is older than
// (now - retention) are deleted. A non-positive retention is treated
// as "no-op" so a misconfigured cron doesn't truncate the table.
//
// Returns the number of rows deleted. The error is the underlying
// pgx error wrapped with %w.
func (s *PostgresStore) Sweep(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		// A zero/negative retention would compute a horizon equal to
		// or after now, deleting every info row in the table. That
		// can't be what the operator wants; treat it as a no-op so a
		// fat-fingered config can't wipe the audit trail.
		return 0, nil
	}
	horizon := s.now().Add(-retention)
	tag, err := s.db.Exec(ctx, sweepSQL, horizon)
	if err != nil {
		return 0, fmt.Errorf("audit: sweep: %w", err)
	}
	return tag.RowsAffected(), nil
}

// isValidActorKind reports whether s is one of the three actor_kind
// enum values the audit_log column accepts. Used to validate the
// Metadata["actor_kind"] override path so callers can't write
// arbitrary strings into the column.
func isValidActorKind(s string) bool {
	switch s {
	case "user", "plugin", "system":
		return true
	default:
		return false
	}
}

// bytesEqual is a tiny helper to avoid pulling in bytes just for one
// equality check inside the row loop.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Ensure PostgresStore satisfies Store at compile time.
var _ Store = (*PostgresStore)(nil)

// Sentinel to make sure errors.Is callers can match the underlying
// pgx errors when needed.
var _ = errors.Is
