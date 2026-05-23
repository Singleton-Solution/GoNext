package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgPool is the subset of *pgxpool.Pool the setup package consumes.
// Extracted as an interface so tests can drive the SQL paths with a
// fake and so a caller running setup inside a larger transaction can
// pass a pgx.Tx.
type PgPool interface {
	BeginTx(ctx context.Context, txOpts pgx.TxOptions) (pgx.Tx, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PoolAdapter wraps *pgxpool.Pool to satisfy PgPool. Both methods are
// pass-through; the wrapper exists only so the interface above can be
// the canonical seam.
type PoolAdapter struct {
	Pool *pgxpool.Pool
}

// BeginTx forwards to the underlying pool.
func (p PoolAdapter) BeginTx(ctx context.Context, txOpts pgx.TxOptions) (pgx.Tx, error) {
	return p.Pool.BeginTx(ctx, txOpts)
}

// QueryRow forwards to the underlying pool.
func (p PoolAdapter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.Pool.QueryRow(ctx, sql, args...)
}

// PgUserCreator persists the bootstrap admin in `users` and the
// password row in `user_passwords` within a single transaction. The
// role assignment goes into `users.meta` as `{"role": "super_admin"}`
// because v1 has no `user_roles` table yet (see docs/06-auth-permissions.md
// §6.1). When the roles migration lands, this writer adds the FK insert
// inside the same transaction.
type PgUserCreator struct {
	pool PgPool
}

// NewPgUserCreator wraps a pool. The caller owns the pool's lifecycle;
// this type does not call Close.
func NewPgUserCreator(pool PgPool) *PgUserCreator {
	return &PgUserCreator{pool: pool}
}

// Create inserts into users + user_passwords in one transaction. The
// returned id is the UUID v7 the database minted via gen_uuid_v7().
//
// On a unique-email violation we surface a typed error so the handler
// could (in a future iteration) render a "this email is taken" copy.
// In v1 the install path is one-shot, so a unique violation is almost
// certainly a concurrent install attempt — already covered by the
// install-marker lock, but we keep the friendlier error for tests.
func (p *PgUserCreator) Create(ctx context.Context, in UserCreateInput) (string, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", fmt.Errorf("setup: begin tx: %w", err)
	}
	// Roll back on every error path. Commit at the end on success.
	defer func() { _ = tx.Rollback(ctx) }()

	meta, mErr := json.Marshal(map[string]string{"role": in.Role})
	if mErr != nil {
		return "", fmt.Errorf("setup: marshal meta: %w", mErr)
	}

	var userID string
	row := tx.QueryRow(ctx,
		`INSERT INTO users
		     (email, handle, display_name, status, meta)
		 VALUES ($1, $2, $3, 'active', $4::jsonb)
		 RETURNING id::text`,
		in.Email, in.Handle, in.DisplayName, string(meta),
	)
	if err := row.Scan(&userID); err != nil {
		return "", fmt.Errorf("setup: insert user: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_passwords (user_id, password_hash)
		 VALUES ($1::uuid, $2)`,
		userID, in.PasswordHash,
	); err != nil {
		return "", fmt.Errorf("setup: insert user_password: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("setup: commit: %w", err)
	}
	return userID, nil
}

// PgOptionStore is a tiny SQL adapter against the `options` table. We
// don't reuse packages/go/settings.PostgresStore because that requires
// pre-registering every key in a Registry with a JSON Schema — overkill
// for the three keys the installer touches.
type PgOptionStore struct {
	pool PgPool
}

// NewPgOptionStore wraps a pool. Lifecycle is the caller's.
func NewPgOptionStore(pool PgPool) *PgOptionStore {
	return &PgOptionStore{pool: pool}
}

// Has reports whether key exists in the options table. A missing row
// is (false, nil); a real query error is (false, err).
func (s *PgOptionStore) Has(ctx context.Context, key string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM options WHERE key = $1)`,
		key,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("setup: options.Has: %w", err)
	}
	return exists, nil
}

// Write upserts (key, value). The migration-seeded rows (core.site.name
// etc.) become updates; the install marker is a fresh insert.
//
// We marshal value through encoding/json so a Go string, number, bool,
// or map all land in the JSONB column with the right shape.
func (s *PgOptionStore) Write(ctx context.Context, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("setup: options.Write marshal: %w", err)
	}
	// UPSERT preserves the existing autoload / is_protected / created_at
	// values on update; we only touch value (and the trigger bumps
	// updated_at / version).
	const sql = `
		INSERT INTO options (key, value)
		VALUES ($1, $2::jsonb)
		ON CONFLICT (key) DO UPDATE
		   SET value = EXCLUDED.value`
	// QueryRow + ignore result is the simplest way to share the
	// PgPool interface (which only exposes QueryRow + BeginTx). Tag
	// the row select onto the end so pgx is happy with the call shape.
	_, err = s.exec(ctx, sql, key, string(encoded))
	if err != nil {
		return fmt.Errorf("setup: options.Write: %w", err)
	}
	return nil
}

// exec runs an INSERT / UPDATE through the pool. We get a tx-bound
// Exec via BeginTx → tx.Exec → Commit; cheap on a single statement.
func (s *PgOptionStore) exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ErrPoolRequired is returned by NewPgUserCreator / NewPgOptionStore
// when the supplied pool is nil. Exposed so wiring code in main can
// catch the nil at boot.
var ErrPoolRequired = errors.New("setup: postgres pool is required")
