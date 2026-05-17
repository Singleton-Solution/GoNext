package audit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_Emit_FillsDefaults(t *testing.T) {
	store := NewMemoryStore()
	fixed := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	store.NowFunc = func() time.Time { return fixed }

	err := store.Emit(context.Background(), Event{
		EventType:   "auth.login.success",
		ActorUserID: "42",
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	events, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len: got %d want 1", len(events))
	}
	got := events[0]
	if got.ID == "" {
		t.Error("ID was not assigned")
	}
	if !got.Time.Equal(fixed) {
		t.Errorf("Time: got %v want %v", got.Time, fixed)
	}
	if got.Severity != SeverityInfo {
		t.Errorf("Severity default: got %q want %q", got.Severity, SeverityInfo)
	}
}

func TestMemoryStore_Emit_RejectsInvalid(t *testing.T) {
	store := NewMemoryStore()

	t.Run("empty EventType", func(t *testing.T) {
		err := store.Emit(context.Background(), Event{})
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("expected ErrInvalidEvent, got %v", err)
		}
	})

	t.Run("unknown severity", func(t *testing.T) {
		err := store.Emit(context.Background(), Event{
			EventType: "x.y",
			Severity:  "loud",
		})
		if !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("expected ErrInvalidEvent, got %v", err)
		}
	})
}

func TestMemoryStore_Emit_TruncatesLongUserAgent(t *testing.T) {
	store := NewMemoryStore()
	long := make([]byte, userAgentMax+500)
	for i := range long {
		long[i] = 'a'
	}
	err := store.Emit(context.Background(), Event{
		EventType: "http.request",
		UserAgent: string(long),
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	events, _ := store.List(context.Background(), Filter{})
	if len(events[0].UserAgent) != userAgentMax {
		t.Errorf("UA len: got %d want %d", len(events[0].UserAgent), userAgentMax)
	}
}

func TestMemoryStore_List_FilterCombinations(t *testing.T) {
	store := NewMemoryStore()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	seed := []Event{
		{EventType: "auth.login.success", ActorUserID: "1", Time: base, Severity: SeverityInfo},
		{EventType: "auth.login.failed", ActorUserID: "", IP: "1.2.3.4", Time: base.Add(1 * time.Minute), Severity: SeverityWarning},
		{EventType: "plugin.activated", ActorPluginSlug: "gn-forms", Time: base.Add(2 * time.Minute), Severity: SeverityInfo},
		{EventType: "policy.denied", ActorUserID: "1", Time: base.Add(3 * time.Minute), Severity: SeverityCritical},
		{EventType: "auth.login.success", ActorUserID: "2", Time: base.Add(4 * time.Minute), Severity: SeverityInfo},
	}
	for _, e := range seed {
		if err := store.Emit(context.Background(), e); err != nil {
			t.Fatalf("seed Emit: %v", err)
		}
	}

	cases := []struct {
		name   string
		filter Filter
		want   int
	}{
		{"no filter returns all", Filter{}, 5},
		{"by event type", Filter{EventType: "auth.login.success"}, 2},
		{"by actor", Filter{ActorUserID: "1"}, 2},
		{"by plugin", Filter{PluginSlug: "gn-forms"}, 1},
		{"by severity", Filter{Severity: SeverityCritical}, 1},
		{"actor + event", Filter{ActorUserID: "1", EventType: "policy.denied"}, 1},
		{"time range half-open start", Filter{Start: base.Add(2 * time.Minute)}, 3},
		{"time range half-open end", Filter{End: base.Add(1 * time.Minute)}, 2},
		{"time range bounded", Filter{Start: base.Add(1 * time.Minute), End: base.Add(3 * time.Minute)}, 3},
		{"actor + plugin (mismatched)", Filter{ActorUserID: "1", PluginSlug: "gn-forms"}, 0},
		{"limit shrinks", Filter{Limit: 2}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := store.List(context.Background(), c.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got) != c.want {
				t.Errorf("len: got %d want %d (events: %+v)", len(got), c.want, got)
			}
		})
	}
}

func TestMemoryStore_List_OrderingNewestFirst(t *testing.T) {
	store := NewMemoryStore()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		err := store.Emit(context.Background(), Event{
			EventType: "x.y",
			Time:      base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	events, _ := store.List(context.Background(), Filter{})
	for i := 1; i < len(events); i++ {
		if !events[i-1].Time.After(events[i].Time) && !events[i-1].Time.Equal(events[i].Time) {
			t.Errorf("order broken at %d: %v then %v", i, events[i-1].Time, events[i].Time)
		}
	}
}

func TestMemoryStore_List_DefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	for i := 0; i < memoryDefaultLimit+50; i++ {
		_ = store.Emit(context.Background(), Event{EventType: "x.y"})
	}
	got, _ := store.List(context.Background(), Filter{}) // no Limit
	if len(got) != memoryDefaultLimit {
		t.Errorf("len: got %d want %d", len(got), memoryDefaultLimit)
	}
}

func TestMemoryStore_ConcurrentEmit(t *testing.T) {
	store := NewMemoryStore()
	var wg sync.WaitGroup
	const writers = 16
	const perWriter = 50
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				_ = store.Emit(context.Background(), Event{EventType: "concurrent.test"})
			}
		}()
	}
	wg.Wait()

	got, _ := store.List(context.Background(), Filter{Limit: writers * perWriter * 2})
	if len(got) != writers*perWriter {
		t.Errorf("len: got %d want %d", len(got), writers*perWriter)
	}
}
