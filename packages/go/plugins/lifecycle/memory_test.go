package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fixedTime is the canonical timestamp used across tests so assertions
// don't have to chase wall-clock drift. UTC is mandatory — every
// production path normalizes to UTC, so test fixtures do the same.
var fixedTime = time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

func newMemForTest(t *testing.T) *MemoryStorage {
	t.Helper()
	s := NewMemoryStorage()
	s.NowFunc = func() time.Time { return fixedTime }
	return s
}

func samplePlugin(slug string) Plugin {
	return Plugin{
		Slug:         slug,
		Version:      "1.0.0",
		ABIVersion:   1,
		State:        StateInstalled,
		Capabilities: []string{"kv", "audit.emit"},
		Manifest:     []byte(`{"slug":"` + slug + `"}`),
	}
}

func TestMemoryStorage_Insert_FillsDefaults(t *testing.T) {
	s := newMemForTest(t)
	p := samplePlugin("gn-seo")

	if err := s.Insert(context.Background(), p); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := s.Get(context.Background(), "gn-seo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.InstalledAt.Equal(fixedTime) {
		t.Errorf("InstalledAt: got %v want %v", got.InstalledAt, fixedTime)
	}
	if !got.UpdatedAt.Equal(fixedTime) {
		t.Errorf("UpdatedAt: got %v want %v", got.UpdatedAt, fixedTime)
	}
	if got.RowVersion != 1 {
		t.Errorf("RowVersion: got %d want 1", got.RowVersion)
	}
}

func TestMemoryStorage_Insert_RejectsDuplicate(t *testing.T) {
	s := newMemForTest(t)
	p := samplePlugin("gn-seo")

	if err := s.Insert(context.Background(), p); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	err := s.Insert(context.Background(), p)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second Insert: got %v want ErrAlreadyExists", err)
	}
}

func TestMemoryStorage_Insert_RejectsInvalidState(t *testing.T) {
	s := newMemForTest(t)
	p := samplePlugin("gn-seo")
	p.State = "" // zero value is invalid

	err := s.Insert(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for invalid state")
	}
}

func TestMemoryStorage_Insert_RejectsEmptySlug(t *testing.T) {
	s := newMemForTest(t)
	p := samplePlugin("")
	err := s.Insert(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for empty slug")
	}
}

func TestMemoryStorage_Get_NotFound(t *testing.T) {
	s := newMemForTest(t)
	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: got %v want ErrNotFound", err)
	}
}

func TestMemoryStorage_Get_ReturnsCopy(t *testing.T) {
	s := newMemForTest(t)
	p := samplePlugin("gn-seo")
	if err := s.Insert(context.Background(), p); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, _ := s.Get(context.Background(), "gn-seo")
	// Mutate the returned copy.
	got.Capabilities[0] = "MUTATED"

	// Storage state should be unchanged.
	again, _ := s.Get(context.Background(), "gn-seo")
	if again.Capabilities[0] == "MUTATED" {
		t.Error("Get returned a live reference; storage was mutated through caller")
	}
}

func TestMemoryStorage_List_SortedBySlug(t *testing.T) {
	s := newMemForTest(t)
	for _, slug := range []string{"gn-zeta", "gn-alpha", "gn-mike"} {
		if err := s.Insert(context.Background(), samplePlugin(slug)); err != nil {
			t.Fatalf("Insert %q: %v", slug, err)
		}
	}
	rows, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"gn-alpha", "gn-mike", "gn-zeta"}
	if len(rows) != len(want) {
		t.Fatalf("len: got %d want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].Slug != w {
			t.Errorf("row %d: got %q want %q", i, rows[i].Slug, w)
		}
	}
}

func TestMemoryStorage_UpdateState_CAS(t *testing.T) {
	s := newMemForTest(t)
	p := samplePlugin("gn-seo")
	if err := s.Insert(context.Background(), p); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Correct precondition: Installed → Active.
	t.Run("happy path", func(t *testing.T) {
		err := s.UpdateState(context.Background(), "gn-seo",
			StateInstalled, StateActive, nil)
		if err != nil {
			t.Fatalf("UpdateState: %v", err)
		}
		got, _ := s.Get(context.Background(), "gn-seo")
		if got.State != StateActive {
			t.Errorf("state: got %q want %q", got.State, StateActive)
		}
		if got.RowVersion != 2 {
			t.Errorf("RowVersion: got %d want 2", got.RowVersion)
		}
	})

	// Wrong precondition — already Active.
	t.Run("wrong precondition", func(t *testing.T) {
		err := s.UpdateState(context.Background(), "gn-seo",
			StateInstalled, StateInactive, nil)
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("got %v want ErrInvalidTransition", err)
		}
	})

	// Row deleted under us.
	t.Run("missing row", func(t *testing.T) {
		err := s.UpdateState(context.Background(), "ghost",
			StateInstalled, StateActive, nil)
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("got %v want ErrInvalidTransition", err)
		}
	})
}

func TestMemoryStorage_UpdateState_AppliesFields(t *testing.T) {
	s := newMemForTest(t)
	p := samplePlugin("gn-seo")
	p.State = StateActive
	if err := s.Insert(context.Background(), p); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	at := time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC)
	reason := "boom"
	fields := &StateUpdateFields{
		LastError: &reason,
		ErrorAt:   &at,
	}
	err := s.UpdateState(context.Background(), "gn-seo",
		StateActive, StateErrored, fields)
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	got, _ := s.Get(context.Background(), "gn-seo")
	if got.LastError != "boom" {
		t.Errorf("LastError: got %q want %q", got.LastError, "boom")
	}
	if !got.ErrorAt.Equal(at) {
		t.Errorf("ErrorAt: got %v want %v", got.ErrorAt, at)
	}
}

func TestMemoryStorage_UpdateState_RejectsInvalidStates(t *testing.T) {
	s := newMemForTest(t)
	if err := s.Insert(context.Background(), samplePlugin("gn-seo")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	t.Run("invalid newState", func(t *testing.T) {
		err := s.UpdateState(context.Background(), "gn-seo", StateInstalled, "weird", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("invalid expectedFrom", func(t *testing.T) {
		err := s.UpdateState(context.Background(), "gn-seo", "weird", StateActive, nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestMemoryStorage_Delete(t *testing.T) {
	s := newMemForTest(t)
	p := samplePlugin("gn-seo")
	if err := s.Insert(context.Background(), p); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := s.Delete(context.Background(), "gn-seo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(context.Background(), "gn-seo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: got %v want ErrNotFound", err)
	}

	// Second delete is ErrNotFound.
	if err := s.Delete(context.Background(), "gn-seo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete: got %v want ErrNotFound", err)
	}
}

// TestMemoryStorage_ConcurrentUpdateState_CAS pounds on UpdateState
// from many goroutines and asserts that exactly one wins. This is the
// race the Manager.Activate path is shielding callers from.
func TestMemoryStorage_ConcurrentUpdateState_CAS(t *testing.T) {
	s := newMemForTest(t)
	if err := s.Insert(context.Background(), samplePlugin("gn-seo")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	const goroutines = 64
	var (
		wg      sync.WaitGroup
		winners int64
		mu      sync.Mutex
	)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			err := s.UpdateState(context.Background(), "gn-seo",
				StateInstalled, StateActive, nil)
			if err == nil {
				mu.Lock()
				winners++
				mu.Unlock()
			} else if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if winners != 1 {
		t.Fatalf("winners: got %d want 1", winners)
	}
}
