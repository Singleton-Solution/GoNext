package passwordreset_test

// Integration test for the password reset flow against a real Postgres
// container. Skipped on hosts without Docker — the in-process tests in
// handler_test.go cover the wire contract; this file exists to catch a
// planner-rejected query shape or a constraint mismatch with the
// migration.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/auth/passwordreset"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

// schemaSQL is the subset of the migration this test needs. Keep in
// sync with migrations/000033_password_reset_tokens.up.sql.
//
// We omit the COMMENT statements and the partial sweep index — the
// tests below exercise the contract, not the planner; the partial
// indexes only matter for the cleanup job which is out of scope here.
const schemaSQL = `
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE OR REPLACE FUNCTION gen_uuid_v7() RETURNS uuid LANGUAGE sql AS $$
  SELECT gen_random_uuid();
$$;

CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    email           CITEXT NOT NULL UNIQUE,
    handle          CITEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_passwords (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash     TEXT NOT NULL,
    params_version    INTEGER NOT NULL DEFAULT 1,
    last_changed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_uuid_v7(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL UNIQUE,
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func setupPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := containers.Postgres(t)
	if dsn == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		t.Fatalf("apply schema: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// insertUser seeds a user row and returns the generated UUID.
func insertUser(t *testing.T, pool *pgxpool.Pool, email, handle string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, handle) VALUES ($1, $2) RETURNING id::text`,
		email, handle,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// TestIntegration_TokenStore_SaveAndConsume_HappyPath inserts a token,
// consumes it, and verifies the row is marked used.
func TestIntegration_TokenStore_SaveAndConsume_HappyPath(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	store, err := passwordreset.NewPgxTokenStore(pool)
	if err != nil {
		t.Fatalf("NewPgxTokenStore: %v", err)
	}

	userID := insertUser(t, pool, "alice@example.com", "alice")
	now := time.Now().UTC()
	hash := "deadbeef" + uuid.NewString()
	if err := store.Save(context.Background(), hash, userID, now.Add(time.Hour)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Consume(context.Background(), hash, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got != userID {
		t.Errorf("returned user_id: got %q, want %q", got, userID)
	}

	// Second consume must fail (single-use).
	if _, err := store.Consume(context.Background(), hash, now.Add(2*time.Minute)); err == nil {
		t.Errorf("second Consume: want error, got nil")
	}

	// Verify the row was updated.
	var usedAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT used_at FROM password_reset_tokens WHERE token_hash = $1`, hash,
	).Scan(&usedAt); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if usedAt == nil {
		t.Errorf("used_at not set after Consume")
	}
}

// TestIntegration_TokenStore_Consume_ExpiredToken verifies the expiry
// check fires at the SQL layer (not just in Go).
func TestIntegration_TokenStore_Consume_ExpiredToken(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	store, err := passwordreset.NewPgxTokenStore(pool)
	if err != nil {
		t.Fatalf("NewPgxTokenStore: %v", err)
	}

	userID := insertUser(t, pool, "bob@example.com", "bob")
	past := time.Now().UTC().Add(-2 * time.Hour)
	hash := "expired-" + uuid.NewString()
	if err := store.Save(context.Background(), hash, userID, past); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := store.Consume(context.Background(), hash, time.Now().UTC()); err == nil {
		t.Errorf("Consume of expired token: want error, got nil")
	}
}

// TestIntegration_UserStore_UpdatePassword inserts a user, runs
// UpdatePassword, and verifies the user_passwords row was rewritten.
func TestIntegration_UserStore_UpdatePassword(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	store, err := passwordreset.NewPgxUserStore(pool)
	if err != nil {
		t.Fatalf("NewPgxUserStore: %v", err)
	}

	userID := insertUser(t, pool, "carol@example.com", "carol")

	// Seed an initial password row.
	_, err = pool.Exec(context.Background(),
		`INSERT INTO user_passwords (user_id, password_hash) VALUES ($1::uuid, $2)`,
		userID, "$argon2id$v=19$m=64,t=1,p=1$AAA$BBB",
	)
	if err != nil {
		t.Fatalf("seed user_passwords: %v", err)
	}

	if err := store.UpdatePassword(context.Background(), userID, "new-hash-here"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	var gotHash string
	if err := pool.QueryRow(context.Background(),
		`SELECT password_hash FROM user_passwords WHERE user_id = $1::uuid`, userID,
	).Scan(&gotHash); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if gotHash != "new-hash-here" {
		t.Errorf("hash: got %q, want %q", gotHash, "new-hash-here")
	}
}

// TestIntegration_UserStore_LookupIDByEmail_CitextSemantics confirms
// the email lookup is case-insensitive (citext column).
func TestIntegration_UserStore_LookupIDByEmail_CitextSemantics(t *testing.T) {
	pool := setupPostgres(t)
	if pool == nil {
		return
	}
	store, err := passwordreset.NewPgxUserStore(pool)
	if err != nil {
		t.Fatalf("NewPgxUserStore: %v", err)
	}

	want := insertUser(t, pool, "Dave@Example.com", "dave")

	got, err := store.LookupIDByEmail(context.Background(), "dave@EXAMPLE.com")
	if err != nil {
		t.Fatalf("LookupIDByEmail: %v", err)
	}
	if got != want {
		t.Errorf("user_id: got %q, want %q", got, want)
	}
}
