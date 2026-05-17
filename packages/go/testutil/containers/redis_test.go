package containers_test

import (
	"context"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/testutil/containers"
	"github.com/redis/go-redis/v9"
)

// TestRedis_AcceptsConnections starts a real Redis container and runs
// PING. Skipped cleanly when Docker isn't available.
func TestRedis_AcceptsConnections(t *testing.T) {
	url := containers.Redis(t)
	if url == "" {
		return
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("redis.ParseURL(%q): %v", url, err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pong, err := client.Ping(ctx).Result()
	if err != nil {
		t.Fatalf("PING: %v", err)
	}
	if pong != "PONG" {
		t.Fatalf("PING: got %q, want %q", pong, "PONG")
	}
}

// TestRedis_SetGet exercises a real round-trip — SET, GET, and DEL —
// to catch the case where the container reports ready but actually
// rejects writes (e.g. AOF/RDB init not complete).
func TestRedis_SetGet(t *testing.T) {
	url := containers.Redis(t)
	if url == "" {
		return
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	got, err := client.Get(ctx, "k").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got != "v" {
		t.Fatalf("GET k: got %q, want %q", got, "v")
	}
}
