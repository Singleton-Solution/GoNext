package redirects

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestEngine_LiteralMatch covers the O(1) literal lookup path.
func TestEngine_LiteralMatch(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	created, err := store.Create(ctx, Rule{
		SourcePath: "/old-page", DestinationPath: "/new-page", Status: 301,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	eng := NewEngine(store)
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	m, ok := eng.Match("/old-page")
	if !ok {
		t.Fatal("Match returned false; expected literal hit")
	}
	if m.Destination != "/new-page" || m.Status != 301 || m.IsRegex {
		t.Fatalf("Match=%+v", m)
	}
	if m.RuleID != created.ID {
		t.Fatalf("RuleID mismatch: got %s want %s", m.RuleID, created.ID)
	}
}

// TestEngine_RegexCaptureSubstitution covers the regex hot path with
// a capture group substituted into the destination.
func TestEngine_RegexCaptureSubstitution(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	if _, err := store.Create(ctx, Rule{
		SourcePath:      "^/blog/(.+)$",
		DestinationPath: "/posts/$1",
		Status:          308,
		IsRegex:         true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	eng := NewEngine(store)
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	m, ok := eng.Match("/blog/hello-world")
	if !ok {
		t.Fatal("regex did not match")
	}
	if m.Destination != "/posts/hello-world" {
		t.Fatalf("Destination=%q want /posts/hello-world", m.Destination)
	}
	if m.Status != 308 || !m.IsRegex {
		t.Fatalf("Match=%+v", m)
	}
}

// TestEngine_LiteralBeatsRegex documents the priority contract: a
// literal rule for the same path wins over a broader regex rule.
func TestEngine_LiteralBeatsRegex(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	if _, err := store.Create(ctx, Rule{
		SourcePath: "^/.*$", DestinationPath: "/catchall", Status: 302, IsRegex: true,
	}); err != nil {
		t.Fatalf("Create regex: %v", err)
	}
	if _, err := store.Create(ctx, Rule{
		SourcePath: "/special", DestinationPath: "/specific", Status: 301,
	}); err != nil {
		t.Fatalf("Create literal: %v", err)
	}
	eng := NewEngine(store)
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	m, ok := eng.Match("/special")
	if !ok || m.Destination != "/specific" || m.Status != 301 {
		t.Fatalf("Match=%+v ok=%v", m, ok)
	}
}

// TestEngine_NoMatch covers the cache-miss path.
func TestEngine_NoMatch(t *testing.T) {
	eng := NewEngine(NewInMemoryStore())
	if _, ok := eng.Match("/nothing-here"); ok {
		t.Fatal("expected no match on empty engine")
	}
}

// TestEngine_HitCountFlush exercises the in-memory counter buffer +
// the explicit Flush call.
func TestEngine_HitCountFlush(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	created, err := store.Create(ctx, Rule{
		SourcePath: "/x", DestinationPath: "/y", Status: 301,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fixed := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	eng := NewEngine(store, WithNowFunc(func() time.Time { return fixed }))
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	for i := 0; i < 7; i++ {
		if _, ok := eng.Match("/x"); !ok {
			t.Fatalf("Match %d: not found", i)
		}
	}
	if got := eng.Stats().PendingHits; got != 7 {
		t.Fatalf("PendingHits=%d want 7", got)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	got, _ := store.Get(ctx, created.ID)
	if got.HitCount != 7 {
		t.Fatalf("HitCount=%d want 7", got.HitCount)
	}
	if !got.LastHitAt.Equal(fixed) {
		t.Fatalf("LastHitAt=%v want %v", got.LastHitAt, fixed)
	}
	// After flush, pending must drain.
	if got := eng.Stats().PendingHits; got != 0 {
		t.Fatalf("PendingHits after Flush=%d want 0", got)
	}
}

// TestEngine_IgnoresUncompilableRegex confirms the engine refuses to
// crash on a bad pattern; the bad rule is silently dropped from the
// index.
func TestEngine_IgnoresUncompilableRegex(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	if _, err := store.Create(ctx, Rule{
		SourcePath: "(unclosed", DestinationPath: "/x", Status: 301, IsRegex: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A valid rule alongside the bad one so we can check the valid
	// one still serves.
	if _, err := store.Create(ctx, Rule{
		SourcePath: "/ok", DestinationPath: "/done", Status: 301,
	}); err != nil {
		t.Fatalf("Create literal: %v", err)
	}
	eng := NewEngine(store)
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, ok := eng.Match("/ok"); !ok {
		t.Fatal("literal rule should still serve when regex was dropped")
	}
}

// TestEngine_RegexCreationOrder asserts first-match-wins follows
// created_at ASC ordering — older regex rules are checked before
// newer ones.
func TestEngine_RegexCreationOrder(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	// Pin the clock so we can be deterministic about creation order.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store.SetNowFunc(func() time.Time { return t0 })
	first, err := store.Create(ctx, Rule{
		SourcePath: "^/a/.*$", DestinationPath: "/first/$0", Status: 301, IsRegex: true,
	})
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	store.SetNowFunc(func() time.Time { return t0.Add(time.Hour) })
	if _, err := store.Create(ctx, Rule{
		SourcePath: "^/a/b$", DestinationPath: "/second", Status: 301, IsRegex: true,
	}); err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	eng := NewEngine(store)
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	m, ok := eng.Match("/a/b")
	if !ok {
		t.Fatal("no match")
	}
	if m.RuleID != first.ID {
		t.Fatalf("expected older rule (%s) to win, got %s", first.ID, m.RuleID)
	}
}

// TestEngine_ConcurrentMatchAndReload races Match calls against
// Reloads + Flushes. -race catches synchronization bugs in the
// atomic index swap and the sync.Map LoadAndDelete sequence.
func TestEngine_ConcurrentMatchAndReload(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if _, err := store.Create(ctx, Rule{
			SourcePath:      "/p" + string(rune('a'+i)),
			DestinationPath: "/q",
			Status:          301,
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	eng := NewEngine(store)
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Match workers.
	for w := 0; w < 8; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := "/p" + string(rune('a'+w%8))
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = eng.Match(path)
				}
			}
		}()
	}
	// Reload + Flush workers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = eng.Reload(ctx)
			_ = eng.Flush(ctx)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Final flush + assert no negative counters.
	_ = eng.Flush(ctx)
	snap, _ := store.Snapshot(ctx)
	for _, r := range snap {
		if r.HitCount < 0 {
			t.Fatalf("HitCount went negative for %s", r.ID)
		}
	}
}

// TestEngine_StartStop covers the background-flusher lifecycle.
func TestEngine_StartStop(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	created, err := store.Create(ctx, Rule{
		SourcePath: "/a", DestinationPath: "/b", Status: 301,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	eng := NewEngine(store, WithFlushInterval(10*time.Millisecond))
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	eng.Start()
	// Issue a hit; wait long enough for at least one flush cycle.
	if _, ok := eng.Match("/a"); !ok {
		t.Fatal("Match miss")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, _ := store.Get(ctx, created.ID)
		if got.HitCount >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := eng.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, _ := store.Get(ctx, created.ID)
	if got.HitCount < 1 {
		t.Fatalf("HitCount=%d want >=1 after background flush", got.HitCount)
	}
}

var _ = uuid.UUID{} // keep the import live; used by sibling files
