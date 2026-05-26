package invalidator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestSubscriber_DispatchesToHandlers exercises the wire path: a
// PUBLISH on the channel parses into (slug, tag) and lands in every
// registered handler.
func TestSubscriber_DispatchesToHandlers(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	const channel = "test:invalidate"
	sub := NewSubscriber(rdb, SubscriberWithChannel(channel))

	var mu sync.Mutex
	var got []string
	sub.Handle(func(_ context.Context, slug, tag string) error {
		mu.Lock()
		got = append(got, slug+"|"+tag)
		mu.Unlock()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = sub.Run(ctx) }()

	// Wait for the subscription to register. miniredis publishes
	// synchronously, but the Subscriber's Receive handshake means
	// the channel registration might not be live for one tick.
	time.Sleep(50 * time.Millisecond)

	for _, msg := range []string{"gn-seo:posts:42", "gnf:menu", "gnf:sitemap"} {
		if err := rdb.Publish(ctx, channel, msg).Err(); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only got %d messages, want 3", n)
		case <-time.After(10 * time.Millisecond):
		}
	}

	want := map[string]bool{
		"gn-seo|posts:42": true,
		"gnf|menu":        true,
		"gnf|sitemap":     true,
	}
	mu.Lock()
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected message %q", g)
		}
	}
	mu.Unlock()
}

// TestSubscriber_MalformedPayloadIsDropped checks that a poison
// message (no colon) does not panic and does not invoke any
// handler. The Subscriber stays alive and consumes the next
// well-formed message normally.
func TestSubscriber_MalformedPayloadIsDropped(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	const channel = "test:invalidate"
	sub := NewSubscriber(rdb, SubscriberWithChannel(channel))

	var calls atomic.Int32
	sub.Handle(func(_ context.Context, _, _ string) error {
		calls.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = sub.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	_ = rdb.Publish(ctx, channel, "no-colon").Err()
	_ = rdb.Publish(ctx, channel, "gnf:menu").Err()

	deadline := time.After(2 * time.Second)
	for calls.Load() < 1 {
		select {
		case <-deadline:
			t.Fatalf("expected one handler call, got %d", calls.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls: got %d, want exactly 1 (malformed dropped)", got)
	}
}

// TestSubscriber_HandlerErrorDoesNotStopLoop confirms an erroring
// handler is logged but the Subscriber keeps consuming. This is the
// at-least-once contract: a flaky handler must not block other
// subscribers on the same channel.
func TestSubscriber_HandlerErrorDoesNotStopLoop(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	const channel = "test:invalidate"
	sub := NewSubscriber(rdb, SubscriberWithChannel(channel))

	var calls atomic.Int32
	sub.Handle(func(_ context.Context, _, _ string) error {
		calls.Add(1)
		return errors.New("boom")
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = sub.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	_ = rdb.Publish(ctx, channel, "gnf:a").Err()
	_ = rdb.Publish(ctx, channel, "gnf:b").Err()

	deadline := time.After(2 * time.Second)
	for calls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("expected at least 2 handler calls, got %d", calls.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestSubscriber_RejectsDoubleRun pins ErrSubscriberAlreadyRunning. A
// second concurrent Run is a programming error — exposed loudly so
// the caller sees the bug at boot.
func TestSubscriber_RejectsDoubleRun(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	sub := NewSubscriber(rdb)
	sub.running.Store(1)
	defer sub.running.Store(0)

	if err := sub.Run(context.Background()); !errors.Is(err, ErrSubscriberAlreadyRunning) {
		t.Fatalf("Run: got %v, want ErrSubscriberAlreadyRunning", err)
	}
}
