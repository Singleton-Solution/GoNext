package migmap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// =============================================================================
// Store interface
// =============================================================================

// Store is the storage tier the migration importer talks to.
// Implementations must be safe for concurrent use; the importer
// fans out per-section workers (authors, posts, terms in parallel)
// and concurrent Put on disjoint keys must not serialise on a
// process-wide mutex.
//
// The interface intentionally takes a Tx parameter on the write
// methods. The importer creates each GoNext entity inside a
// transaction and records the mapping in the *same* transaction —
// if the entity insert rolls back, the mapping must roll back too.
// Pass nil to use the store's default pool; pass a real transaction
// to enrol the write in the caller's commit boundary.
//
// Get is intentionally NOT transactional: lookups run outside the
// per-entity tx so a row inserted by an earlier transaction is
// visible to the current one, even if the current tx is still
// in flight.
type Store interface {
	// Put records a mapping. On (source, entity_type, source_id)
	// conflict it MERGES meta with the existing row and PRESERVES
	// the original target_id and imported_at — re-imports do not
	// re-mint the GoNext UUID.
	//
	// Returns ErrInvalidMapping (wrapped) for structurally invalid
	// input. Other errors are transport failures from the database
	// driver.
	Put(ctx context.Context, tx Tx, m Mapping) error

	// PutBatch records multiple mappings in a single round-trip.
	// Empty input is a no-op. The semantics for each individual
	// mapping match [Put]. The whole batch shares the same Tx —
	// either all rows are committed or none.
	//
	// Validation runs against every mapping before the first SQL
	// statement is issued, so an invalid mapping at the tail of the
	// slice doesn't leave the head half-inserted.
	PutBatch(ctx context.Context, tx Tx, ms []Mapping) error

	// Get returns the mapping for the (source, entityType, sourceID)
	// tuple. The (nil, false, nil) tuple means "no such mapping" —
	// callers MUST distinguish this from an error (database down).
	// Returning a nil error on a miss is the convention every store
	// in this codebase follows; see audit.Store.List for the same
	// pattern.
	Get(ctx context.Context, source Source, entityType EntityType, sourceID string) (*Mapping, bool, error)

	// GetByTarget returns every mapping that points at targetID. The
	// common case is a single result, but in principle multiple
	// source systems may alias to one GoNext entity (a manual merge)
	// — the reverse lookup must therefore be a slice, not an Option.
	//
	// Order is unspecified. Callers that care about a stable order
	// should sort client-side.
	GetByTarget(ctx context.Context, targetID uuid.UUID) ([]Mapping, error)
}

// =============================================================================
// Tx abstraction
// =============================================================================

// Tx is the minimum surface [PostgresStore] needs from a transaction
// or connection. pgx.Tx, pgxpool.Tx, and pgxpool.Pool all satisfy it,
// which means callers can pass:
//
//   - a *pgx.Tx, to enrol the mapping write in their own commit
//   - a *pgxpool.Pool (via the [poolAsTx] adapter), for one-shot writes
//     outside any explicit transaction
//   - nil, which the store treats as "use my default pool"
//
// The interface deliberately omits CommitTx / RollbackTx — the store
// never opens or finishes a transaction on the caller's behalf. That
// keeps the rollback semantics under the importer's control.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// =============================================================================
// PostgresStore
// =============================================================================

// PostgresStore is the durable tier. Schema lives in
// migrations/000015_migration_map.up.sql.
//
// The struct holds a pool used as the default Tx when the caller
// passes nil. Tests can construct it with [NewPostgresStore] over a
// real pgxpool.Pool, or use [newPostgresStoreForTest] to substitute
// a fake Tx implementation without booting Postgres.
type PostgresStore struct {
	pool Tx
}

// NewPostgresStore wires a [PostgresStore] over any Tx-compatible
// connection or pool. The common production case is to pass
// *pgxpool.Pool directly — it satisfies Tx out of the box.
func NewPostgresStore(pool Tx) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// resolveTx picks the Tx the next SQL statement runs against. When
// the caller passes a non-nil tx we enrol the write in their
// transaction; otherwise we fall back to the store's default pool.
// Returning an error for the (nil pool + nil tx) case prevents a
// nil-pointer panic deep inside pgx.
func (s *PostgresStore) resolveTx(tx Tx) (Tx, error) {
	if tx != nil {
		return tx, nil
	}
	if s.pool == nil {
		return nil, fmt.Errorf("migmap: no transaction and no default pool configured")
	}
	return s.pool, nil
}

// Put inserts the mapping or merges it with the existing row.
//
// The ON CONFLICT clause is the load-bearing part of this method:
//
//   - target_id is PRESERVED (the first import wins). Re-importing a
//     WXR file must not change the UUID we minted on pass 1 — every
//     downstream reference (post.author, term_relationships) is keyed
//     on it.
//
//   - meta is MERGED (jsonb concatenation). A second-pass importer
//     can add `revisions_imported_at` without destroying the
//     first-pass `original_login`.
//
//   - imported_at is PRESERVED. It records when the entity was first
//     seen, not when it was last touched — useful for "which posts
//     came from the Jan import vs the March re-import?" forensics.
func (s *PostgresStore) Put(ctx context.Context, tx Tx, m Mapping) error {
	if err := m.Validate(); err != nil {
		return err
	}
	conn, err := s.resolveTx(tx)
	if err != nil {
		return err
	}
	meta, err := marshalMeta(m.Meta)
	if err != nil {
		return fmt.Errorf("migmap.Put: marshal meta: %w", err)
	}
	const q = `
		INSERT INTO migration_map (source, entity_type, source_id, target_id, meta)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		ON CONFLICT (source, entity_type, source_id) DO UPDATE
			SET meta = migration_map.meta || EXCLUDED.meta
	`
	if _, err := conn.Exec(ctx, q, string(m.Source), string(m.EntityType), m.SourceID, m.TargetID, meta); err != nil {
		return fmt.Errorf("migmap.Put: %w", err)
	}
	return nil
}

// PutBatch inserts multiple rows in one statement. We expand the
// VALUES list rather than issuing N statements because the
// importer's hot path is "insert 10k term mappings between two big
// SELECTs" and the round-trip cost dominates the per-row cost.
//
// pgx's named-parameter syntax doesn't let us batch with a single
// $1-style placeholder — we generate the placeholders inline. The
// growth is linear in batch size; the parameter cap of ~32k means
// a single call comfortably handles 6k rows (5 params per row).
// Callers expecting more should chunk; the importer does this in
// practice.
func (s *PostgresStore) PutBatch(ctx context.Context, tx Tx, ms []Mapping) error {
	if len(ms) == 0 {
		return nil
	}
	// Validate eagerly so a bad row at index N doesn't leave rows
	// 0..N-1 half-applied.
	for i, m := range ms {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("migmap.PutBatch[%d]: %w", i, err)
		}
	}
	conn, err := s.resolveTx(tx)
	if err != nil {
		return err
	}

	// Build the VALUES clause and the args slice in lockstep so we
	// don't drift the placeholder indices off the args slice.
	const colsPerRow = 5
	args := make([]any, 0, len(ms)*colsPerRow)
	sql := batchInsertSQL(len(ms))
	for _, m := range ms {
		meta, err := marshalMeta(m.Meta)
		if err != nil {
			return fmt.Errorf("migmap.PutBatch: marshal meta: %w", err)
		}
		args = append(args, string(m.Source), string(m.EntityType), m.SourceID, m.TargetID, meta)
	}
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("migmap.PutBatch: %w", err)
	}
	return nil
}

// Get fetches one mapping by its natural key. Returns
// (nil, false, nil) for a miss; the caller uses the bool, not the
// error, to branch.
func (s *PostgresStore) Get(ctx context.Context, source Source, entityType EntityType, sourceID string) (*Mapping, bool, error) {
	if s.pool == nil {
		return nil, false, fmt.Errorf("migmap.Get: no pool configured")
	}
	const q = `
		SELECT target_id, meta
		FROM migration_map
		WHERE source = $1 AND entity_type = $2 AND source_id = $3
	`
	var target uuid.UUID
	var metaRaw []byte
	err := s.pool.QueryRow(ctx, q, string(source), string(entityType), sourceID).Scan(&target, &metaRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("migmap.Get: %w", err)
	}
	meta, err := unmarshalMeta(metaRaw)
	if err != nil {
		return nil, false, fmt.Errorf("migmap.Get: unmarshal meta: %w", err)
	}
	return &Mapping{
		Source:     source,
		EntityType: entityType,
		SourceID:   sourceID,
		TargetID:   target,
		Meta:       meta,
	}, true, nil
}

// GetByTarget returns every mapping with target_id = targetID.
// Empty slice on a miss; the call always succeeds for a connected
// database. The reverse-lookup index makes this an indexed scan.
func (s *PostgresStore) GetByTarget(ctx context.Context, targetID uuid.UUID) ([]Mapping, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("migmap.GetByTarget: no pool configured")
	}
	const q = `
		SELECT source, entity_type, source_id, meta
		FROM migration_map
		WHERE target_id = $1
	`
	// Use Query (not QueryRow) — multi-row reverse lookups are the
	// whole point of this method. pgx's Query method is also exposed
	// on pgxpool.Pool, but our Tx interface intentionally doesn't
	// declare it: GetByTarget is read-only so it always uses the
	// underlying pool, not a caller-supplied tx.
	type queryer interface {
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	}
	qPool, ok := s.pool.(queryer)
	if !ok {
		return nil, fmt.Errorf("migmap.GetByTarget: pool does not implement Query")
	}
	rows, err := qPool.Query(ctx, q, targetID)
	if err != nil {
		return nil, fmt.Errorf("migmap.GetByTarget: %w", err)
	}
	defer rows.Close()

	var out []Mapping
	for rows.Next() {
		var source, entityType, sourceID string
		var metaRaw []byte
		if err := rows.Scan(&source, &entityType, &sourceID, &metaRaw); err != nil {
			return nil, fmt.Errorf("migmap.GetByTarget: scan: %w", err)
		}
		meta, err := unmarshalMeta(metaRaw)
		if err != nil {
			return nil, fmt.Errorf("migmap.GetByTarget: unmarshal meta: %w", err)
		}
		out = append(out, Mapping{
			Source:     Source(source),
			EntityType: EntityType(entityType),
			SourceID:   sourceID,
			TargetID:   targetID,
			Meta:       meta,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migmap.GetByTarget: rows: %w", err)
	}
	return out, nil
}

// =============================================================================
// Helpers
// =============================================================================

// batchInsertSQL produces the parameterised INSERT for n rows. The
// placeholders are 1-indexed and emitted in groups of five matching
// the column order in the Exec call.
func batchInsertSQL(n int) string {
	// Pre-size the builder to avoid the repeated grow that string
	// concatenation would trigger. 80 chars per row is a generous
	// upper bound for the ($1,$2,$3,$4,$5::jsonb) form once the
	// indices reach four digits.
	header := "INSERT INTO migration_map (source, entity_type, source_id, target_id, meta) VALUES "
	const trailer = " ON CONFLICT (source, entity_type, source_id) DO UPDATE SET meta = migration_map.meta || EXCLUDED.meta"
	buf := make([]byte, 0, len(header)+n*40+len(trailer))
	buf = append(buf, header...)
	for i := 0; i < n; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		base := i*5 + 1
		buf = append(buf, '(')
		buf = appendPlaceholder(buf, base)
		buf = append(buf, ',')
		buf = appendPlaceholder(buf, base+1)
		buf = append(buf, ',')
		buf = appendPlaceholder(buf, base+2)
		buf = append(buf, ',')
		buf = appendPlaceholder(buf, base+3)
		buf = append(buf, ',')
		buf = appendPlaceholder(buf, base+4)
		buf = append(buf, "::jsonb)"...)
	}
	buf = append(buf, trailer...)
	return string(buf)
}

// appendPlaceholder writes "$N" for a small N without going through
// fmt or strconv. Hot path during batch construction; the importer
// can issue this on thousands of rows.
func appendPlaceholder(buf []byte, n int) []byte {
	buf = append(buf, '$')
	if n < 10 {
		return append(buf, byte('0'+n))
	}
	// Reverse-and-flip the digits; allocation-free.
	var tmp [10]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	return append(buf, tmp[i:]...)
}

// marshalMeta produces the JSONB payload for a Mapping.Meta map.
// Nil and empty maps both serialise to "{}" so the column never
// stores NULL (the schema rejects it anyway via DEFAULT).
func marshalMeta(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

// unmarshalMeta turns the BYTEA from the database back into a Go map.
// Empty / NULL / "{}" all collapse to a nil map — the caller treats
// nil and empty interchangeably.
func unmarshalMeta(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "{}" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
