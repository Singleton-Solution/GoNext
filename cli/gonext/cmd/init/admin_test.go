package initcmd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Singleton-Solution/GoNext/packages/go/auth/password"
	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
)

func TestLocalPart(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"alice@example.com", "alice"},
		{"admin@localhost", "admin"},
		{"no-at-sign", "no-at-sign"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := localPart(tc.in); got != tc.want {
			t.Errorf("localPart(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCreateAdmin_PasswordTooShort(t *testing.T) {
	_, err := createAdmin(context.Background(), nil, adminInput{
		email:    "a@example.com",
		password: "short",
		pepper:   []byte("p"),
	})
	if !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("err=%v want ErrPasswordTooShort", err)
	}
}

func TestCreateAdmin_BadEmail(t *testing.T) {
	_, err := createAdmin(context.Background(), nil, adminInput{
		email:    "not-an-email",
		password: "verylongpassword12",
		pepper:   []byte("p"),
	})
	if !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("err=%v want ErrInvalidEmail", err)
	}
}

func TestCreateAdmin_HappyPath(t *testing.T) {
	t.Parallel()
	dsn := containers.Postgres(t)
	if dsn == "" {
		t.Skip("docker not available")
	}
	root := repoRoot(t)
	applyMigrations(t, dsn, filepath.Join(root, "migrations"))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	pepper := []byte("the-pepper-is-secret-and-mixed-in-with-hmac")
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	id, err := createAdmin(ctx, tx, adminInput{
		email:    "owner@example.com",
		password: "test-pass-12-chars",
		pepper:   pepper,
	})
	if err != nil {
		t.Fatalf("createAdmin: %v", err)
	}
	if id.String() == "00000000-0000-0000-0000-000000000000" {
		t.Errorf("returned id is zero")
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Verify the password hash round-trips with the same pepper.
	var hash string
	if err := pool.QueryRow(ctx, `
		SELECT password_hash FROM user_passwords WHERE user_id = $1
	`, id).Scan(&hash); err != nil {
		t.Fatalf("query password: %v", err)
	}
	ok, _, err := password.Verify("test-pass-12-chars", hash, pepper)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("password.Verify returned false")
	}

	// Re-running should refuse with ErrAdminExists.
	tx2, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	_, err = createAdmin(ctx, tx2, adminInput{
		email:    "owner@example.com",
		password: "another-long-pass-12",
		pepper:   pepper,
	})
	if !errors.Is(err, ErrAdminExists) {
		t.Errorf("expected ErrAdminExists, got %v", err)
	}

	// Email match should be case-insensitive (citext).
	tx3, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx3: %v", err)
	}
	defer func() { _ = tx3.Rollback(ctx) }()
	_, err = createAdmin(ctx, tx3, adminInput{
		email:    "OWNER@EXAMPLE.COM",
		password: "another-long-pass-12",
		pepper:   pepper,
	})
	if !errors.Is(err, ErrAdminExists) {
		t.Errorf("expected case-insensitive ErrAdminExists, got %v", err)
	}
}
