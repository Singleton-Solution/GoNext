package session

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
	"github.com/redis/go-redis/v9"
)

// newTestManager returns a Manager wired to the Redis instance at
// REDIS_URL, or skips the test if the variable is unset. The caller is
// given a per-test logical DB carve-out via FLUSHDB.
//
// This intentionally avoids requiring a docker-compose or testcontainers
// dependency in the unit test path — CI provisions a Redis sidecar and
// exports REDIS_URL.
func newTestManager(t *testing.T) (*Manager, func()) {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m, err := New(ctx, config.RedisConfig{URL: url}, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Tests share whatever DB the URL points at; clean it before and
	// after so we don't poison neighbours.
	if err := m.rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("FlushDB: %v", err)
	}
	cleanup := func() {
		fctx, fcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer fcancel()
		_ = m.rdb.FlushDB(fctx).Err()
		_ = m.Close()
	}
	return m, cleanup
}

func TestNew_RequiresURL(t *testing.T) {
	_, err := New(context.Background(), config.RedisConfig{}, nil)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestNew_BadURL(t *testing.T) {
	_, err := New(context.Background(), config.RedisConfig{URL: "::::not-a-url"}, nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestManager_CreateGetRoundTrip(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	token, err := m.Create(ctx, "user-1", map[string]any{
		"factors": []any{"password", "totp"},
		"role":    "admin",
	}, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !validToken(token) {
		t.Fatalf("Create returned malformed token: %q", token)
	}

	sess, err := m.Get(ctx, token, 15*time.Minute)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sess.UserID != "user-1" {
		t.Errorf("UserID: got %q want user-1", sess.UserID)
	}
	if sess.Token != token {
		t.Errorf("Token not echoed back: got %q want %q", sess.Token, token)
	}
	if sess.Data["role"] != "admin" {
		t.Errorf("Data[role]: got %v want admin", sess.Data["role"])
	}
	if sess.CreatedAt.IsZero() || sess.LastSeenAt.IsZero() {
		t.Errorf("timestamps not set: %+v", sess)
	}
}

func TestManager_CreateValidatesArgs(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	cases := []struct {
		name    string
		userID  string
		ttl     time.Duration
		idle    time.Duration
		wantErr bool
	}{
		{"empty userID", "", time.Hour, time.Minute, true},
		{"zero ttl", "u", 0, time.Minute, true},
		{"negative ttl", "u", -1, time.Minute, true},
		{"zero idle", "u", time.Hour, 0, true},
		{"idle > ttl", "u", time.Minute, time.Hour, true},
		{"ok", "u", time.Hour, time.Minute, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Create(ctx, tc.userID, nil, tc.ttl, tc.idle)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestManager_GetUnknownIsErrNotFound(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	// A well-formed but unknown token.
	tok, _ := generateToken()
	_, err := m.Get(ctx, tok, time.Minute)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestManager_GetInvalidToken(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	_, err := m.Get(ctx, "not-a-token", time.Minute)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestManager_TTLExpires(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	// 1 second idle TTL, 5 second absolute. Wait it out.
	tok, err := m.Create(ctx, "user-ttl", nil, 5*time.Second, time.Second)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	_, err = m.Get(ctx, tok, time.Second)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("after TTL: err = %v, want ErrNotFound", err)
	}
}

func TestManager_IdleTTLRefreshedOnGet(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	tok, err := m.Create(ctx, "user-idle", nil, time.Hour, 2*time.Second)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Wait long enough to halve the idle window, then Get — refreshes.
	time.Sleep(1200 * time.Millisecond)
	if _, err := m.Get(ctx, tok, 2*time.Second); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Wait long enough that without a refresh, the key would have died.
	time.Sleep(1200 * time.Millisecond)
	if _, err := m.Get(ctx, tok, 2*time.Second); err != nil {
		t.Fatalf("post-refresh Get: %v, want nil (idle TTL not refreshed?)", err)
	}
}

func TestManager_GetUpdatesLastSeenAt(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	tok, err := m.Create(ctx, "user-ls", nil, time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := m.Get(ctx, tok, 30*time.Minute)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	second, err := m.Get(ctx, tok, 30*time.Minute)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !second.LastSeenAt.After(first.LastSeenAt) {
		t.Errorf("LastSeenAt did not advance: first=%v second=%v",
			first.LastSeenAt, second.LastSeenAt)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt drifted: first=%v second=%v",
			first.CreatedAt, second.CreatedAt)
	}
}

func TestManager_Delete(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	tok, err := m.Create(ctx, "user-del", nil, time.Hour, time.Minute)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Delete(ctx, tok); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(ctx, tok, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: err = %v, want ErrNotFound", err)
	}
	// The user-sessions set should no longer carry this token.
	mems, err := m.rdb.SMembers(ctx, userSessionsKey("user-del")).Result()
	if err != nil {
		t.Fatalf("SMembers: %v", err)
	}
	for _, t2 := range mems {
		if t2 == tok {
			t.Errorf("token still in user_sessions set after Delete")
		}
	}
}

func TestManager_Delete_Idempotent(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	tok, _ := generateToken()
	if err := m.Delete(ctx, tok); err != nil {
		t.Fatalf("Delete on unknown token: %v", err)
	}
}

func TestManager_Delete_InvalidToken(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	err := m.Delete(ctx, "garbage")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestManager_DeleteAllForUser(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	var toks []string
	for i := 0; i < 3; i++ {
		tok, err := m.Create(ctx, "user-multi", nil, time.Hour, time.Minute)
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		toks = append(toks, tok)
	}
	if err := m.DeleteAllForUser(ctx, "user-multi"); err != nil {
		t.Fatalf("DeleteAllForUser: %v", err)
	}
	for _, tok := range toks {
		if _, err := m.Get(ctx, tok, time.Minute); !errors.Is(err, ErrNotFound) {
			t.Fatalf("token %q still live after revoke-all: err=%v", tok, err)
		}
	}
	// The user-sessions set should be empty (or absent).
	n, err := m.rdb.SCard(ctx, userSessionsKey("user-multi")).Result()
	if err != nil {
		t.Fatalf("SCard: %v", err)
	}
	if n != 0 {
		t.Errorf("user-sessions set: %d members remain, want 0", n)
	}
}

func TestManager_List(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	want := map[string]bool{}
	for i := 0; i < 3; i++ {
		tok, err := m.Create(ctx, "user-list", map[string]any{"i": i},
			time.Hour, 30*time.Minute)
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		want[tok] = true
	}

	infos, err := m.List(ctx, "user-list")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("List length: got %d want 3", len(infos))
	}
	for _, info := range infos {
		if !want[info.Token] {
			t.Errorf("List returned unexpected token %q", info.Token)
		}
		if info.UserID != "user-list" {
			t.Errorf("UserID: got %q want user-list", info.UserID)
		}
		if info.CreatedAt.IsZero() || info.LastSeenAt.IsZero() {
			t.Errorf("timestamps zero: %+v", info)
		}
	}
}

func TestManager_List_PrunesStale(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	tok, err := m.Create(ctx, "user-stale", nil, time.Hour, 30*time.Minute)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Forcibly remove the session blob but leave the user-sessions
	// pointer behind, simulating an expired-key race.
	if err := m.rdb.Del(ctx, sessionKey(tok)).Err(); err != nil {
		t.Fatalf("Del session blob: %v", err)
	}
	infos, err := m.List(ctx, "user-stale")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("List should skip stale entries, got %d", len(infos))
	}
	// Second call: the pointer should now be gone.
	n, err := m.rdb.SCard(ctx, userSessionsKey("user-stale")).Result()
	if err != nil {
		t.Fatalf("SCard: %v", err)
	}
	if n != 0 {
		t.Errorf("stale pointer not pruned, %d members remain", n)
	}
}

func TestManager_List_Empty(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()
	ctx := context.Background()

	infos, err := m.List(ctx, "nobody")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("got %d, want 0", len(infos))
	}
}

func TestManager_Close_Idempotent(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL not set; skipping")
	}
	ctx := context.Background()
	m, err := New(ctx, config.RedisConfig{URL: url}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close should not panic; go-redis returns ErrClosed.
	_ = m.Close()
}

// Compile-time assertion: a Manager value is the right shape for the
// public surface the issue requires. If anyone removes a method by
// accident this fails to compile.
var _ = func() *Manager {
	var m *Manager
	var ctx context.Context
	_, _ = m.Create(ctx, "", nil, 0, 0)
	_, _ = m.Get(ctx, "", 0)
	_, _ = m.Regenerate(ctx, "", 0)
	_ = m.Delete(ctx, "")
	_ = m.DeleteAllForUser(ctx, "")
	_, _ = m.List(ctx, "")
	m.SetMaxDataSize(0)
	_ = m.Close()
	return m
}

// Quiet unused-import linter on go-redis when REDIS_URL is unset and
// every integration test skips at the top.
var _ = redis.Nil
