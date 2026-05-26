package audit

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/audit"
)

func newTestStore(t *testing.T) (audit.Store, func()) {
	t.Helper()
	s := audit.NewMemoryStore()
	return s, func() {}
}

func TestTail_InitialDump(t *testing.T) {
	t.Parallel()
	store, closeFn := newTestStore(t)
	defer closeFn()

	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		_ = store.Emit(context.Background(), audit.Event{
			Time:        now.Add(time.Duration(i) * time.Second),
			EventType:   "test.event",
			ActorUserID: "alice",
		})
	}

	deps := tailDeps{
		openStore: func(ctx context.Context) (audit.Store, func(), error) { return store, func() {}, nil },
		now:       func() time.Time { return now.Add(10 * time.Minute) },
	}
	var stdout, stderr bytes.Buffer
	code := runTailWithDeps([]string{"--limit", "10"}, &stdout, &stderr, deps)
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	got := stdout.String()
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 5 {
		t.Errorf("lines = %d, want 5; got=%q", len(lines), got)
	}
	if !strings.Contains(lines[0], "test.event") {
		t.Errorf("missing event_type: %q", lines[0])
	}
}

func TestTail_JSONMode(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	_ = store.Emit(context.Background(), audit.Event{
		Time:      now,
		EventType: "auth.login.success",
	})
	deps := tailDeps{
		openStore: func(ctx context.Context) (audit.Store, func(), error) { return store, func() {}, nil },
		now:       func() time.Time { return now.Add(time.Minute) },
	}
	var stdout, stderr bytes.Buffer
	code := runTailWithDeps([]string{"--json"}, &stdout, &stderr, deps)
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), `"EventType":"auth.login.success"`) {
		t.Errorf("json line missing: %s", stdout.String())
	}
}

func TestTail_Filter_Severity(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	_ = store.Emit(context.Background(), audit.Event{
		Time: now, EventType: "info.event", Severity: audit.SeverityInfo,
	})
	_ = store.Emit(context.Background(), audit.Event{
		Time: now.Add(time.Second), EventType: "crit.event", Severity: audit.SeverityCritical,
	})

	deps := tailDeps{
		openStore: func(ctx context.Context) (audit.Store, func(), error) { return store, func() {}, nil },
		now:       func() time.Time { return now.Add(time.Minute) },
	}
	var stdout, stderr bytes.Buffer
	code := runTailWithDeps([]string{"--severity", "critical"}, &stdout, &stderr, deps)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout.String(), "info.event") {
		t.Errorf("severity filter let info through: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "crit.event") {
		t.Errorf("severity filter missed critical: %s", stdout.String())
	}
}

func TestTail_InvalidLimit(t *testing.T) {
	t.Parallel()
	deps := tailDeps{
		openStore: func(ctx context.Context) (audit.Store, func(), error) { return audit.NewMemoryStore(), func() {}, nil },
		now:       time.Now,
	}
	var stdout, stderr bytes.Buffer
	code := runTailWithDeps([]string{"--limit", "10000"}, &stdout, &stderr, deps)
	if code != ExitUsage {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestTail_Follow(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	_ = store.Emit(context.Background(), audit.Event{
		Time: now, EventType: "initial.event",
	})

	signals := make(chan os.Signal, 1)
	deps := tailDeps{
		openStore: func(ctx context.Context) (audit.Store, func(), error) { return store, func() {}, nil },
		now:       func() time.Time { return now.Add(10 * time.Minute) },
		tickEvery: 5 * time.Millisecond,
		signals:   signals,
	}

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runTailWithDeps([]string{"--follow"}, &stdout, &stderr, deps)
	}()

	// Let the initial dump complete + a few ticks.
	time.Sleep(50 * time.Millisecond)
	_ = store.Emit(context.Background(), audit.Event{
		Time:      now.Add(11 * time.Minute),
		EventType: "fresh.event",
	})
	time.Sleep(50 * time.Millisecond)
	close(signals)

	select {
	case code := <-done:
		if code != ExitOK {
			t.Errorf("exit = %d, want 0; stderr=%s", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not exit on signal")
	}
	if !strings.Contains(stdout.String(), "initial.event") {
		t.Errorf("missing initial event: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "fresh.event") {
		t.Errorf("missing follow event: %s", stdout.String())
	}
}
