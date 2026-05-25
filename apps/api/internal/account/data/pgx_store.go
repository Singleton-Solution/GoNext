package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
)

// PgxAnonymizer is the production [Anonymizer] backed by Postgres.
//
// The Anonymize call runs inside a single transaction so that a crash
// mid-update leaves the user EITHER fully intact OR fully anonymised —
// never half-zeroed with the email still visible. We escalate to
// REPEATABLE READ isolation because two concurrent delete requests
// from the same user (the panic-tap scenario) must not interleave.
//
// PII zeroed:
//   - users.email             → '<userID>@deleted.invalid' (preserves uniqueness)
//   - users.handle            → 'deleted-<userID-prefix>'  (UI fallback)
//   - users.display_name      → 'Deleted User'
//   - users.bio, avatar_url   → NULL
//   - users.meta              → '{}'::jsonb
//   - users.status            → 'deleted'
//   - users.anonymized_at     → now()
//   - users.scheduled_purge_at → now() + 30d
//
// Authored content is RE-OWNED to a sentinel id (the constant
// AnonymousAuthorID) rather than deleted: GDPR's right to erasure
// applies to PII, not to content the deleted user produced and which
// other users have already replied to. This mirrors the well-known
// "Deleted User" pattern from forum software.
type PgxAnonymizer struct {
	pool *pgxpool.Pool
}

// NewPgxAnonymizer wraps a pool. Caller owns Close().
func NewPgxAnonymizer(pool *pgxpool.Pool) *PgxAnonymizer {
	if pool == nil {
		panic("data.NewPgxAnonymizer: pool is required")
	}
	return &PgxAnonymizer{pool: pool}
}

// AnonymousAuthorID is the sentinel user id that owns content
// previously authored by deleted users. The id is loaded from
// migrations/000002_users.up.sql by a follow-up seed; we keep the
// constant here so the handler doesn't need to consult the database
// to know what value to write.
//
// Empty string disables re-ownership and posts/comments are
// soft-deleted instead. Operators wire the live value through their
// own config; the package default leaves it empty so the in-memory
// tests stay self-contained.
var AnonymousAuthorID = ""

// purgeWindow mirrors the same constant in the handler. Keeping a
// local copy lets the store run without importing the handler file's
// private symbol.
const purgeWindow = 30 * 24 * time.Hour

// Anonymize implements the [Anonymizer] interface.
func (s *PgxAnonymizer) Anonymize(ctx context.Context, userID string) error {
	if userID == "" {
		return ErrUserNotFound
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadWrite,
	})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Rollback on any error path. tx.Commit later replaces the rollback;
	// after a successful commit Rollback is a no-op.
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	purgeAt := now.Add(purgeWindow)

	// Lock the row up front so concurrent delete attempts serialise.
	var existingAnonymizedAt *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT anonymized_at FROM users WHERE id = $1::uuid FOR UPDATE`,
		userID,
	).Scan(&existingAnonymizedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("select user: %w", err)
	}
	if existingAnonymizedAt != nil {
		return ErrAlreadyAnonymized
	}

	// Stamp the user row. The email format keeps the citext UNIQUE
	// constraint satisfied without leaking the original address into
	// the new value (we substitute the user id).
	zeroedEmail := fmt.Sprintf("%s@deleted.invalid", userID)
	zeroedHandle := fmt.Sprintf("deleted-%s", userID[:8])
	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET email              = $2,
		    handle             = $3,
		    display_name       = 'Deleted User',
		    bio                = NULL,
		    avatar_url         = NULL,
		    meta               = '{}'::jsonb,
		    status             = 'deleted',
		    anonymized_at      = $4,
		    scheduled_purge_at = $5
		WHERE id = $1::uuid
	`, userID, zeroedEmail, zeroedHandle, now, purgeAt); err != nil {
		return fmt.Errorf("update user: %w", err)
	}

	// Wipe the password row — there is no scenario in which the
	// anonymised user logs in again, and keeping a hash around would
	// keep PII (the params and the salt) alive past the purge window.
	if _, err := tx.Exec(ctx,
		`DELETE FROM user_passwords WHERE user_id = $1::uuid`, userID,
	); err != nil {
		return fmt.Errorf("delete password: %w", err)
	}

	// Re-own posts and comments to the anonymous sentinel if one is
	// configured. The UPDATE is best-effort: tables may not exist in
	// every deployment (the comments migration #29 is gated on a
	// feature flag in some environments).
	if AnonymousAuthorID != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE posts SET author_id = $2::uuid WHERE author_id = $1::uuid`,
			userID, AnonymousAuthorID,
		); err != nil {
			// Posts table is mandatory — surface the failure.
			return fmt.Errorf("reown posts: %w", err)
		}
		// Comments are optional; ignore "relation does not exist".
		if _, err := tx.Exec(ctx,
			`UPDATE comments SET author_id = $2::uuid WHERE author_id = $1::uuid`,
			userID, AnonymousAuthorID,
		); err != nil && !isUndefinedTable(err) {
			return fmt.Errorf("reown comments: %w", err)
		}
	}

	// Zero PII columns on the audit log without deleting the rows —
	// the rows themselves remain as the forensic record of the
	// account's history.
	if _, err := tx.Exec(ctx, `
		UPDATE audit_log
		SET ip = NULL, user_agent = NULL
		WHERE actor_user_id = $1::uuid
	`, userID); err != nil && !isUndefinedTable(err) {
		return fmt.Errorf("zero audit pii: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// isUndefinedTable returns true if the error is a "relation does not
// exist" failure. Helps us tolerate missing optional tables in
// stripped-down deployments without swallowing genuine errors.
func isUndefinedTable(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps PGcode in pgconn.PgError; we keep the import out of
	// the function signature by string-matching on the SQLSTATE prefix.
	// 42P01 = undefined_table.
	return err != nil && (containsAll(err.Error(), "42P01") || containsAll(err.Error(), "undefined_table"))
}

func containsAll(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// --- password verifier ------------------------------------------------

// PgxPasswordVerifier implements [PasswordVerifier] by reading the
// argon2id PHC string from user_passwords and delegating to
// packages/go/auth/password.Verify.
type PgxPasswordVerifier struct {
	pool   *pgxpool.Pool
	pepper []byte
}

// NewPgxPasswordVerifier wraps a pool with the cluster-wide argon2id
// pepper. The pepper is the same value the login handler uses; passing
// the wrong one here means every delete attempt fails with
// invalid_password, which is the safe failure mode.
func NewPgxPasswordVerifier(pool *pgxpool.Pool, pepper []byte) *PgxPasswordVerifier {
	if pool == nil {
		panic("data.NewPgxPasswordVerifier: pool is required")
	}
	return &PgxPasswordVerifier{pool: pool, pepper: pepper}
}

// Verify implements [PasswordVerifier].
func (v *PgxPasswordVerifier) Verify(ctx context.Context, userID, plaintext string) (bool, error) {
	if userID == "" || plaintext == "" {
		return false, nil
	}
	var encoded string
	err := v.pool.QueryRow(ctx,
		`SELECT password_hash FROM user_passwords WHERE user_id = $1::uuid`,
		userID,
	).Scan(&encoded)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No password row → cannot verify. Treat as failure rather
			// than error so the handler returns 401.
			return false, nil
		}
		return false, fmt.Errorf("select password: %w", err)
	}
	ok, _, err := password.Verify(plaintext, encoded, v.pepper)
	if err != nil {
		return false, fmt.Errorf("verify: %w", err)
	}
	return ok, nil
}
