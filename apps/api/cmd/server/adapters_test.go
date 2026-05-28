// Regression tests for the login adapters in adapters.go (issue #521).
//
// PR #496 (and its follow-up in PR #523) wired users.meta.roles into the
// session principal via the SQL lookup adapters in adapters.go. Without
// the COALESCE + JSON unmarshal on the SELECT, super_admin users lost
// their role on every sign-in — the session would mint, but every
// capability check would deny.
//
// These tests pin the projection: both userLookupByEmail (the password
// path) and userLookupByID (the TOTP finalize path) must populate
// UserRecord.Roles from users.meta.roles. They use a testcontainers
// Postgres instance because the COALESCE-on-jsonb shape isn't worth
// faking — pgxmock would require us to hand-roll the JSON encoding and
// would not catch a planner regression.
//
// Skipped under `go test -short`; nightly-full-tests covers the full
// run.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/auth/login"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// adaptersSchemaSQL is the slice of the migration tree these tests
// need. Kept in sync with `migrations/000001_*` (users + user_passwords)
// and the role-meta column already present on users since the bootstrap
// migration.
//
// We deliberately omit unrelated tables — the lookup queries only touch
// `users` and `user_passwords` so anything else would just add noise to
// the failure mode if a column ever renames.
const adaptersSchemaSQL = `
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE OR REPLACE FUNCTION gen_uuid_v7() RETURNS uuid LANGUAGE sql AS $$
  SELECT gen_random_uuid();
$$;

CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    email      CITEXT NOT NULL UNIQUE,
    handle     CITEXT NOT NULL UNIQUE,
    status     TEXT NOT NULL DEFAULT 'active',
    meta       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_passwords (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash   TEXT NOT NULL,
    params_version  INTEGER NOT NULL DEFAULT 1,
    last_changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func setupAdaptersPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		// containers.Postgres already called t.Skip; bail out.
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if _, err := pool.Exec(ctx, adaptersSchemaSQL); err != nil {
		pool.Close()
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// insertUser inserts a user with the given email + roles meta and an
// optional password hash. Returns the generated UUID as a string.
func insertUser(t *testing.T, pool *pgxpool.Pool, email, handle, status string, rolesJSON string, hash string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id string
	q := `
		INSERT INTO users (email, handle, status, meta)
		VALUES ($1::citext, $2::citext, $3, jsonb_build_object('roles', $4::jsonb))
		RETURNING id::text
	`
	if err := pool.QueryRow(ctx, q, email, handle, status, rolesJSON).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if hash != "" {
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_passwords (user_id, password_hash) VALUES ($1::uuid, $2)
		`, id, hash); err != nil {
			t.Fatalf("insert user_password: %v", err)
		}
	}
	return id
}

// TestUserLookupByEmail_ProjectsRolesFromMeta_Issue521 pins the contract
// PR #496 + PR #523 fixed: the by-email adapter MUST populate Roles
// from users.meta.roles. Without this projection a super_admin who
// signs in via password loses their role on session creation.
func TestUserLookupByEmail_ProjectsRolesFromMeta_Issue521(t *testing.T) {
	pool := setupAdaptersPostgres(t)
	if pool == nil {
		return
	}
	insertUser(t, pool,
		"admin@example.com",
		"admin",
		"active",
		`["super_admin","editor"]`,
		"$argon2id$v=19$m=65536,t=3,p=4$YWFh$YWFhYWFh",
	)

	lookup := userLookupByEmail(pool)
	rec, err := lookup(context.Background(), "admin@example.com")
	if err != nil {
		t.Fatalf("lookup: unexpected error %v", err)
	}
	if rec.Email != "admin@example.com" {
		t.Errorf("Email = %q, want %q", rec.Email, "admin@example.com")
	}
	if rec.Status != "active" {
		t.Errorf("Status = %q, want %q", rec.Status, "active")
	}
	if rec.Hash == "" {
		t.Error("Hash should be populated (user_passwords row exists)")
	}
	if got, want := len(rec.Roles), 2; got != want {
		t.Fatalf("len(Roles) = %d, want %d (roles: %v)", got, want, rec.Roles)
	}
	wantRoles := map[string]bool{"super_admin": true, "editor": true}
	for _, r := range rec.Roles {
		if !wantRoles[r] {
			t.Errorf("unexpected role %q in %v", r, rec.Roles)
		}
		delete(wantRoles, r)
	}
	if len(wantRoles) != 0 {
		t.Errorf("missing roles: %v (have %v)", wantRoles, rec.Roles)
	}
}

// TestUserLookupByID_ProjectsRolesFromMeta_Issue521 is the TOTP-path
// sibling. The TOTP finalize handler only has a user id (recovered from
// the intermediate token), so a separate by-id lookup exists. The two
// MUST be case-equivalent — same projection, same Status, same Roles.
// Without this the TOTP path silently dropped roles even after the
// password path was fixed.
func TestUserLookupByID_ProjectsRolesFromMeta_Issue521(t *testing.T) {
	pool := setupAdaptersPostgres(t)
	if pool == nil {
		return
	}
	id := insertUser(t, pool,
		"totp@example.com",
		"totp",
		"active",
		`["super_admin"]`,
		"$argon2id$v=19$m=65536,t=3,p=4$YWFh$YWFhYWFh",
	)

	lookup := userLookupByID(pool)
	rec, err := lookup(context.Background(), id)
	if err != nil {
		t.Fatalf("lookup by id: unexpected error %v", err)
	}
	if rec.ID != id {
		t.Errorf("ID = %q, want %q", rec.ID, id)
	}
	if rec.Email != "totp@example.com" {
		t.Errorf("Email = %q, want %q", rec.Email, "totp@example.com")
	}
	if rec.Status != "active" {
		t.Errorf("Status = %q, want %q", rec.Status, "active")
	}
	if len(rec.Roles) != 1 || rec.Roles[0] != "super_admin" {
		t.Errorf("Roles = %v, want [super_admin]", rec.Roles)
	}
}

// TestUserLookupByEmail_EmptyRolesArray_Issue521 covers the case
// where meta.roles is the default empty array. The lookup must return
// a zero-length Roles slice (not nil-vs-empty matters: the session
// data map encodes []string verbatim, and downstream code reads .len).
func TestUserLookupByEmail_EmptyRolesArray_Issue521(t *testing.T) {
	pool := setupAdaptersPostgres(t)
	if pool == nil {
		return
	}
	insertUser(t, pool,
		"noroles@example.com",
		"noroles",
		"active",
		`[]`,
		"",
	)

	lookup := userLookupByEmail(pool)
	rec, err := lookup(context.Background(), "noroles@example.com")
	if err != nil {
		t.Fatalf("lookup: unexpected error %v", err)
	}
	if len(rec.Roles) != 0 {
		t.Errorf("Roles = %v, want [] for user with empty meta.roles", rec.Roles)
	}
	// OAuth-only user (no password row) → empty Hash, NOT an error.
	if rec.Hash != "" {
		t.Errorf("Hash = %q, want empty for OAuth-only user", rec.Hash)
	}
}

// TestUserLookupByEmail_MissingRolesKey_Issue521 covers the COALESCE
// branch: when meta.roles is missing entirely (legacy rows), the SQL
// projects '[]'::jsonb. The lookup must not error and must return a
// zero-length Roles slice — the same behavior as an explicit empty
// array.
func TestUserLookupByEmail_MissingRolesKey_Issue521(t *testing.T) {
	pool := setupAdaptersPostgres(t)
	if pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Insert a user with meta = '{}' so meta->'roles' is NULL.
	// We bypass insertUser's jsonb_build_object('roles', …) call so
	// the meta is the empty object — the regression target.
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, handle, status, meta)
		VALUES ('legacy@example.com'::citext, 'legacy'::citext, 'active', '{}'::jsonb)
		RETURNING id::text
	`).Scan(&id)
	if err != nil {
		t.Fatalf("insert legacy user: %v", err)
	}

	lookup := userLookupByEmail(pool)
	rec, lookupErr := lookup(context.Background(), "legacy@example.com")
	if lookupErr != nil {
		t.Fatalf("lookup: unexpected error %v", lookupErr)
	}
	if len(rec.Roles) != 0 {
		t.Errorf("Roles = %v, want [] for user with missing meta.roles", rec.Roles)
	}
}

// TestUserLookupByEmail_NotFound_Issue521 pins the contract that an
// unknown email returns login.ErrUserNotFound (sentinel), not a wrapped
// pgx.ErrNoRows. The service uses errors.Is(err, ErrUserNotFound) for
// the constant-time hash-or-not branch.
func TestUserLookupByEmail_NotFound_Issue521(t *testing.T) {
	pool := setupAdaptersPostgres(t)
	if pool == nil {
		return
	}
	lookup := userLookupByEmail(pool)
	_, err := lookup(context.Background(), "ghost@example.com")
	if err == nil {
		t.Fatal("lookup: want error for missing user, got nil")
	}
	if err != login.ErrUserNotFound {
		t.Errorf("err = %v, want login.ErrUserNotFound", err)
	}
}

// TestUserLookupByID_NotFound_Issue521 is the by-id sibling — same
// sentinel contract.
func TestUserLookupByID_NotFound_Issue521(t *testing.T) {
	pool := setupAdaptersPostgres(t)
	if pool == nil {
		return
	}
	lookup := userLookupByID(pool)
	// A syntactically valid UUID that doesn't exist in the table.
	_, err := lookup(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("lookup by id: want error for missing user, got nil")
	}
	if err != login.ErrUserNotFound {
		t.Errorf("err = %v, want login.ErrUserNotFound", err)
	}
}
