package verify

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// checkUsers compares every source Author against its destination
// users row. Per author:
//
//   - existence (looked up by login → handle, or by email)
//   - email preserved verbatim (when present)
//   - meta.must_reset_password is set (the importer always writes
//     this; if it's missing the row was almost certainly not
//     imported by this code path)
//
// Plus a cardinality check: count of imported authors >= source
// count. We allow extras because the importer creates a synthetic
// "migrated" user on the fly for posts whose Creator wasn't
// declared in the WXR preamble.
func (v *Verifier) checkUsers(ctx context.Context, st *runState, report *Report) error {
	// Cardinality. We expect at least len(st.authors); the
	// importer may have added the synthetic "migrated" user too.
	var got int
	row := v.DB.QueryRow(ctx, `
		SELECT count(*)
		  FROM users
		 WHERE (meta->>'must_reset_password')::boolean = true
	`)
	if err := row.Scan(&got); err != nil {
		return wrapVerifyErr("users.count", err)
	}
	if got >= len(st.authors) {
		report.AddPass("users.count")
	} else {
		report.AddFailure(Failure{
			CheckName: "users.count",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("user count too low: source=%d db=%d", len(st.authors), got),
			Source:    fmt.Sprintf("%d", len(st.authors)),
			Target:    fmt.Sprintf("%d", got),
		})
	}

	for _, a := range st.authors {
		if err := v.checkOneAuthor(ctx, a.Login, a.Email, report); err != nil {
			return err
		}
	}
	return nil
}

// checkOneAuthor probes one author by login (preferred) or email.
func (v *Verifier) checkOneAuthor(ctx context.Context, login, email string, report *Report) error {
	login = strings.TrimSpace(login)
	email = strings.TrimSpace(email)
	if login == "" && email == "" {
		// Nothing to look up; the importer would have failed too.
		return nil
	}

	var (
		id        uuid.UUID
		dbEmail   string
		mustReset *bool
	)
	err := v.DB.QueryRow(ctx, `
		SELECT id, email::text, (meta->>'must_reset_password')::boolean
		  FROM users
		 WHERE ($1::citext <> '' AND handle = $1::citext)
		    OR ($2::citext <> '' AND email = $2::citext)
		 LIMIT 1
	`, login, email).Scan(&id, &dbEmail, &mustReset)
	if errors.Is(err, pgx.ErrNoRows) {
		report.AddFailure(Failure{
			CheckName: "users.exists",
			Severity:  SeverityError,
			Reason:    fmt.Sprintf("author not found: login=%q email=%q", login, email),
			Source:    login,
		})
		return nil
	}
	if err != nil {
		return wrapVerifyErr("users.probe", err)
	}

	// Email preserved (unless the source had none, in which case
	// the importer synthesises a placeholder we don't penalise).
	if email != "" {
		if strings.EqualFold(dbEmail, email) {
			report.AddPass("users.email")
		} else {
			report.AddFailure(Failure{
				CheckName: "users.email",
				Severity:  SeverityError,
				Reason:    fmt.Sprintf("email mismatch: source=%q db=%q", email, dbEmail),
				Source:    login,
				Target:    id.String(),
			})
		}
	}

	// must_reset_password flag.
	if mustReset != nil && *mustReset {
		report.AddPass("users.must_reset")
	} else {
		report.AddFailure(Failure{
			CheckName: "users.must_reset",
			Severity:  SeverityError,
			Reason:    "meta.must_reset_password is not true",
			Source:    login,
			Target:    id.String(),
		})
	}
	return nil
}
