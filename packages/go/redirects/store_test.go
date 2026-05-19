package redirects

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestInMemoryStore_CreateRoundTrip covers the happy path: insert,
// fetch, list, snapshot all see the same row.
func TestInMemoryStore_CreateRoundTrip(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	created, err := s.Create(ctx, Rule{
		SourcePath:      "/old",
		DestinationPath: "/new",
		Status:          301,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("Create did not assign an ID")
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("Create did not stamp CreatedAt")
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SourcePath != "/old" || got.DestinationPath != "/new" {
		t.Fatalf("Get returned %+v", got)
	}

	snap, err := s.Snapshot(ctx)
	if err != nil || len(snap) != 1 {
		t.Fatalf("Snapshot: %v len=%d", err, len(snap))
	}
}

// TestInMemoryStore_RejectsInvalid asserts validateForCreate fires.
func TestInMemoryStore_RejectsInvalid(t *testing.T) {
	s := NewInMemoryStore()
	cases := []Rule{
		{SourcePath: "", DestinationPath: "/new", Status: 301},
		{SourcePath: "/old", DestinationPath: "", Status: 301},
		{SourcePath: "/old", DestinationPath: "/new", Status: 200},
		{SourcePath: "/old", DestinationPath: "/new", Status: 0},
	}
	for i, r := range cases {
		if _, err := s.Create(context.Background(), r); !errors.Is(err, ErrInvalidRule) {
			t.Fatalf("case %d: expected ErrInvalidRule, got %v", i, err)
		}
	}
}

// TestInMemoryStore_DuplicateRejected asserts the UNIQUE on
// (source_path, is_regex) is enforced.
func TestInMemoryStore_DuplicateRejected(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	rule := Rule{SourcePath: "/old", DestinationPath: "/new", Status: 301}
	if _, err := s.Create(ctx, rule); err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if _, err := s.Create(ctx, rule); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
	// Same source, but is_regex=true is a DIFFERENT rule — must succeed.
	rule.IsRegex = true
	if _, err := s.Create(ctx, rule); err != nil {
		t.Fatalf("regex variant should be allowed: %v", err)
	}
}

// TestInMemoryStore_UpdateAndDelete covers the mutation path.
func TestInMemoryStore_UpdateAndDelete(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	created, err := s.Create(ctx, Rule{
		SourcePath: "/old", DestinationPath: "/new", Status: 301,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated := created
	updated.Status = 302
	updated.DestinationPath = "/newer"
	got, err := s.Update(ctx, updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Status != 302 || got.DestinationPath != "/newer" {
		t.Fatalf("Update result %+v", got)
	}
	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete expected ErrNotFound, got %v", err)
	}
	// Delete is idempotent.
	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete idempotency: %v", err)
	}
}

// TestInMemoryStore_BulkIncrementHits asserts the counter flush path.
func TestInMemoryStore_BulkIncrementHits(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	created, err := s.Create(ctx, Rule{
		SourcePath: "/old", DestinationPath: "/new", Status: 301,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now := time.Now()
	if err := s.BulkIncrementHits(ctx, []HitDelta{{
		RuleID: created.ID, Count: 5, LastHitAt: now,
	}}); err != nil {
		t.Fatalf("BulkIncrementHits: %v", err)
	}
	got, _ := s.Get(ctx, created.ID)
	if got.HitCount != 5 {
		t.Fatalf("HitCount=%d want 5", got.HitCount)
	}
	if !got.LastHitAt.Equal(now) {
		t.Fatalf("LastHitAt=%v want %v", got.LastHitAt, now)
	}
	// Empty input is a no-op.
	if err := s.BulkIncrementHits(ctx, nil); err != nil {
		t.Fatalf("empty: %v", err)
	}
	// Delta for a deleted rule is silently dropped.
	gone := uuid.New()
	if err := s.BulkIncrementHits(ctx, []HitDelta{{RuleID: gone, Count: 1, LastHitAt: now}}); err != nil {
		t.Fatalf("ghost delta: %v", err)
	}
}

// TestInMemoryStore_Concurrent exercises the read-write mix the
// engine actually generates. We're more interested in race-clean
// behavior (via -race) than in throughput.
func TestInMemoryStore_Concurrent(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	// Seed a handful of rules.
	ids := make([]uuid.UUID, 0, 4)
	for i := 0; i < 4; i++ {
		r, err := s.Create(ctx, Rule{
			SourcePath:      "/p" + string(rune('a'+i)),
			DestinationPath: "/q",
			Status:          301,
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids = append(ids, r.ID)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = s.Snapshot(ctx)
				_, _ = s.List(ctx, time.Time{}, 10)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			deltas := make([]HitDelta, 0, len(ids))
			for _, id := range ids {
				deltas = append(deltas, HitDelta{RuleID: id, Count: 1, LastHitAt: time.Now()})
			}
			_ = s.BulkIncrementHits(ctx, deltas)
		}
	}()
	wg.Wait()
}
