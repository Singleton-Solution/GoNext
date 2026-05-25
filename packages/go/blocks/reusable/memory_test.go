package reusable

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryStore_CreateRoundTrip(t *testing.T) {
	clock := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	s := NewMemoryStoreWithClock(func() time.Time { return clock })

	created, err := s.Create(context.Background(), Entry{
		Name:    "Pricing CTA",
		Attrs:   json.RawMessage(`{"icon":"dollar"}`),
		Content: json.RawMessage(`[{"type":"core/paragraph","attributes":{"text":"hi"}}]`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("Create did not assign an ID")
	}
	if !created.CreatedAt.Equal(clock) || !created.UpdatedAt.Equal(clock) {
		t.Fatalf("Create did not stamp timestamps: %+v", created)
	}

	got, err := s.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Pricing CTA" {
		t.Fatalf("Get returned %+v", got)
	}
}

func TestMemoryStore_CreateRejectsInvalid(t *testing.T) {
	s := NewMemoryStore()
	cases := []Entry{
		{Name: ""},
		{Name: "valid", Attrs: json.RawMessage(`not-json`)},
		{Name: "valid", Content: json.RawMessage(`{still bad`)},
	}
	for i, e := range cases {
		if _, err := s.Create(context.Background(), e); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("case %d: expected ErrInvalidEntry, got %v", i, err)
		}
	}
}

func TestMemoryStore_UpdateAndDelete(t *testing.T) {
	clock := time.Now().UTC().Truncate(time.Second)
	tick := func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	s := NewMemoryStoreWithClock(tick)

	created, err := s.Create(context.Background(), Entry{Name: "v1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := created
	updated.Name = "v2"
	got, err := s.Update(context.Background(), updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Name != "v2" {
		t.Fatalf("Update did not change name: %+v", got)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("Update did not re-stamp UpdatedAt: %v vs %v", got.UpdatedAt, created.UpdatedAt)
	}

	if err := s.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: expected ErrNotFound, got %v", err)
	}
	// Idempotent delete.
	if err := s.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("idempotent Delete: %v", err)
	}
}

func TestMemoryStore_UpdateUnknown(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.Update(context.Background(), Entry{ID: uuid.New(), Name: "x"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_List_OrderAndFilter(t *testing.T) {
	clock := time.Now().UTC().Truncate(time.Second)
	tick := func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	s := NewMemoryStoreWithClock(tick)
	names := []string{"alpha", "beta-1", "BETA-2", "gamma"}
	for _, n := range names {
		if _, err := s.Create(context.Background(), Entry{Name: n}); err != nil {
			t.Fatalf("Create %q: %v", n, err)
		}
	}

	// Default list returns newest first.
	all, err := s.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List len = %d, want 4", len(all))
	}
	if all[0].Name != "gamma" || all[3].Name != "alpha" {
		t.Fatalf("List order wrong: %v", []string{all[0].Name, all[3].Name})
	}

	// Substring filter is case-insensitive.
	filt, err := s.List(context.Background(), ListFilter{NameContains: "beta"})
	if err != nil {
		t.Fatalf("List filt: %v", err)
	}
	if len(filt) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filt))
	}

	// Limit caps the result.
	cap, err := s.List(context.Background(), ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("List cap: %v", err)
	}
	if len(cap) != 2 {
		t.Fatalf("cap len = %d, want 2", len(cap))
	}
}

func TestMemoryStore_GetMany(t *testing.T) {
	s := NewMemoryStore()
	ids := make([]uuid.UUID, 3)
	for i := range ids {
		e, err := s.Create(context.Background(), Entry{Name: "n"})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids[i] = e.ID
	}
	// Mix one unknown ID in.
	requested := []uuid.UUID{ids[0], uuid.New(), ids[1], ids[2]}
	got, err := s.GetMany(context.Background(), requested)
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("GetMany returned %d, want 3", len(got))
	}
}

func TestMemoryStore_ConcurrentReadsAndWrites(t *testing.T) {
	s := NewMemoryStore()
	for i := 0; i < 10; i++ {
		if _, err := s.Create(context.Background(), Entry{Name: "n"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = s.List(context.Background(), ListFilter{})
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		_, _ = s.Create(context.Background(), Entry{Name: "n"})
	}
	<-done
}
