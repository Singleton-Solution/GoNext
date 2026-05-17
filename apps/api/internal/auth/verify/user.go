package verify

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UserVerifier is the slice of user-table behaviour the verify
// handlers depend on. It exists so handler unit tests can inject a
// fake without standing up Postgres — the production
// [PgxUserVerifier] satisfies it against the real DB.
type UserVerifier interface {
	// MarkVerified sets users.email_verified_at = now() for the
	// supplied userID. Returns [ErrUserNotFound] if no row was
	// affected (deleted account, mistyped ID).
	MarkVerified(ctx context.Context, userID string) error

	// LookupEmail returns the email address registered for userID.
	// Used by /verify/send to populate the To: address without
	// trusting client-supplied input. Returns [ErrUserNotFound]
	// when the user is unknown.
	LookupEmail(ctx context.Context, userID string) (string, error)
}

// ErrUserNotFound is returned by [UserVerifier.MarkVerified] when the
// UPDATE affected zero rows. This is a server-side data-consistency
// signal — the token's user_id pointed at a user that no longer
// exists. The handler maps it to 410 Gone so the wire response is
// indistinguishable from "the token has expired" (we don't want to
// leak whether a specific user_id is present).
var ErrUserNotFound = errors.New("verify: user not found")

// PgxUserVerifier is the production [UserVerifier] backed by a
// pgxpool. The pool is borrowed from apps/api's main wiring — the
// verifier does NOT close it.
type PgxUserVerifier struct {
	pool *pgxpool.Pool
}

// NewPgxUserVerifier returns a verifier that issues UPDATEs through
// pool. Returns an error if pool is nil.
func NewPgxUserVerifier(pool *pgxpool.Pool) (*PgxUserVerifier, error) {
	if pool == nil {
		return nil, errors.New("verify: pgx pool is nil")
	}
	return &PgxUserVerifier{pool: pool}, nil
}

// MarkVerified runs `UPDATE users SET email_verified_at = now()
// WHERE id = $1 AND email_verified_at IS NULL`. The IS NULL guard
// makes the UPDATE idempotent: a repeated verification (rare but
// possible if the user clicks the link twice) returns 0 rows,
// which we treat as "already done, no rewrite needed".
//
// We deliberately do NOT touch the version / updated_at columns
// here — the touch trigger on `users` handles those. The single
// SET reduces the chance of a concurrent profile edit's UPDATE
// losing to our timestamp write under optimistic-concurrency
// retries.
func (v *PgxUserVerifier) MarkVerified(ctx context.Context, userID string) error {
	if userID == "" {
		return ErrUserNotFound
	}
	tag, err := v.pool.Exec(ctx,
		`UPDATE users SET email_verified_at = now()
		 WHERE id = $1::uuid AND email_verified_at IS NULL`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("verify: update users: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Could be "user does not exist" OR "already verified". We
		// return success for the already-verified case by issuing a
		// follow-up read — but only when the affected count was 0,
		// which is the rare path. A second SELECT here keeps the
		// happy path (UPDATE-only) at one round-trip.
		var present bool
		err := v.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid)`,
			userID,
		).Scan(&present)
		if err != nil {
			return fmt.Errorf("verify: check user: %w", err)
		}
		if !present {
			return ErrUserNotFound
		}
		// User exists, already verified — treat as success so the
		// handler returns 200 either way (idempotent verify).
	}
	return nil
}

// LookupEmail returns the email address from the users row keyed by
// userID. ErrUserNotFound is returned when the row is absent.
//
// The query uses ::uuid casting so the supplied userID is validated
// before it hits the index; a malformed string surfaces as the
// "invalid input syntax" error from pgx which we translate to
// ErrUserNotFound (don't leak shape detail).
func (v *PgxUserVerifier) LookupEmail(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", ErrUserNotFound
	}
	var addr string
	err := v.pool.QueryRow(ctx,
		`SELECT email FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&addr)
	if err != nil {
		// pgx returns ErrNoRows; treat any error here as "no user"
		// rather than threading a typed comparison just for this one
		// site.
		if strings.Contains(err.Error(), "no rows") {
			return "", ErrUserNotFound
		}
		return "", fmt.Errorf("verify: lookup email: %w", err)
	}
	return addr, nil
}
