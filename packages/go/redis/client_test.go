package redis

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/config"
)

// quietLogger returns a slog logger that discards all output. Tests
// that don't care about log lines use this.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// dsnFromEnv returns a usable REDIS_URL for integration tests. We
// honor the same env var the production loader does. CI sets this
// via a redis service; local devs set it via docker-compose.
//
// If unset, integration tests skip — they don't fail and don't try
// to fall back to a hard-coded localhost. CI will fail the unit
// cohort either way, but the contributor running `go test ./...`
// without a running Redis should see a SKIP, not an unexplained
// failure.
func dsnFromEnv(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("REDIS_URL")
	if dsn == "" {
		t.Skip("REDIS_URL not set; skipping integration test")
	}
	return dsn
}

func TestNew_RequiresURL(t *testing.T) {
	_, err := New(context.Background(), config.RedisConfig{URL: ""}, quietLogger())
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "REDIS_URL") {
		t.Errorf("error should mention REDIS_URL, got: %v", err)
	}
}

func TestNew_MalformedURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"not a url", "not a valid url"},
		{"wrong scheme", "http://localhost:6379"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(context.Background(), config.RedisConfig{URL: tt.url}, quietLogger())
			if err == nil {
				t.Fatalf("expected error for %q", tt.url)
			}
			if !strings.Contains(err.Error(), "parse") {
				t.Errorf("error should mention parse, got: %v", err)
			}
		})
	}
}

func TestNew_DSNNotLeakedInError(t *testing.T) {
	// A Redis DSN can contain a password (redis://:pass@host:port). The
	// error returned by New must NOT echo the DSN back — operators
	// paste error messages into chat all the time.
	const secret = "hunter2-very-secret-password"
	dsn := "redis://:" + secret + "@nope.invalid:9999/0"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := New(ctx, config.RedisConfig{
		URL:         dsn,
		DialTimeout: 500 * time.Millisecond,
	}, quietLogger())
	if err == nil {
		t.Fatal("expected ping to fail against bogus host")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks password: %v", err)
	}
}

func TestNew_NilLoggerTolerated(t *testing.T) {
	// We don't actually call New here — we'd hit DNS — but we exercise
	// the empty-URL path that runs the nil-logger branch before any
	// network I/O.
	_, err := New(context.Background(), config.RedisConfig{URL: ""}, nil)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	// Reaching this point without panic is the assertion.
}

func TestSplitHostPort(t *testing.T) {
	tests := []struct {
		in   string
		host string
		port string
	}{
		{"localhost:6379", "localhost", "6379"},
		{"127.0.0.1:6398", "127.0.0.1", "6398"},
		{"[::1]:6379", "::1", "6379"},
		{"garbage-no-port", "?", "?"},
		{"", "?", "?"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			h, p := splitHostPort(tt.in)
			if h != tt.host || p != tt.port {
				t.Errorf("splitHostPort(%q) = (%q, %q), want (%q, %q)",
					tt.in, h, p, tt.host, tt.port)
			}
		})
	}
}

// --- Integration tests below: require a real Redis ---

func TestNew_IntegrationPing(t *testing.T) {
	dsn := dsnFromEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := New(ctx, config.RedisConfig{
		URL:          dsn,
		PoolSize:     5,
		MinIdleConns: 1,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}, quietLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Ping after New: %v", err)
	}

	// Round-trip a SET/GET to verify the client actually talks to a
	// real server, not just a half-open socket.
	const key = "gonext:redis:smoke"
	if err := client.Set(ctx, key, "ok", 30*time.Second).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	defer func() { _ = client.Del(ctx, key).Err() }()

	got, err := client.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

func TestNew_IntegrationPoolSize(t *testing.T) {
	dsn := dsnFromEnv(t)

	client, err := New(context.Background(), config.RedisConfig{
		URL:          dsn,
		PoolSize:     7,
		MinIdleConns: 2,
	}, quietLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = client.Close() }()

	if got := client.Options().PoolSize; got != 7 {
		t.Errorf("PoolSize: got %d, want 7", got)
	}
	if got := client.Options().MinIdleConns; got != 2 {
		t.Errorf("MinIdleConns: got %d, want 2", got)
	}
}

// helper sentinel — the only sanity check we actually exercise here
// is that we never accidentally suppress real errors. Reserved for a
// future package-error type.
var _ = errors.Is
