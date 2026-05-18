package cron

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// TestRegister_RoundTrip is the happy-path smoke test: register a
// spec, fetch it back, observe the same scalar fields.
func TestRegister_RoundTrip(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	spec := CronSpec{
		Name:     "demo.daily",
		Schedule: "0 3 * * *",
		TaskName: "demo.task",
	}
	if err := r.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("demo.daily")
	if !ok {
		t.Fatal("Get: expected spec present after Register")
	}
	if got.Name != spec.Name || got.Schedule != spec.Schedule || got.TaskName != spec.TaskName {
		t.Errorf("Get: scalar fields differ: got %+v, want %+v", got, spec)
	}
}

// TestRegister_EmptyNameRejected pins the ErrEmptyName contract.
func TestRegister_EmptyNameRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	err := r.Register(CronSpec{Name: "", Schedule: "@daily", TaskName: "x"})
	if !errors.Is(err, ErrEmptyName) {
		t.Fatalf("Register(empty Name): got %v, want ErrEmptyName", err)
	}
}

// TestRegister_EmptyScheduleRejected pins the ErrEmptySchedule contract.
func TestRegister_EmptyScheduleRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	err := r.Register(CronSpec{Name: "x", Schedule: "", TaskName: "x"})
	if !errors.Is(err, ErrEmptySchedule) {
		t.Fatalf("Register(empty Schedule): got %v, want ErrEmptySchedule", err)
	}
}

// TestRegister_EmptyTaskNameRejected pins the ErrEmptyTaskName contract.
func TestRegister_EmptyTaskNameRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	err := r.Register(CronSpec{Name: "x", Schedule: "@daily", TaskName: ""})
	if !errors.Is(err, ErrEmptyTaskName) {
		t.Fatalf("Register(empty TaskName): got %v, want ErrEmptyTaskName", err)
	}
}

// TestRegister_InvalidScheduleRejected pins the ErrInvalidSchedule
// contract. A bad expression must fail at register time so the bug
// shows up at boot, not at first fire.
func TestRegister_InvalidScheduleRejected(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	bad := []string{
		"not a cron expression",
		"0 25 * * *", // hour 25 is invalid
		"*/0 * * * *",
		"",
	}
	for _, expr := range bad {
		if expr == "" {
			continue // covered by TestRegister_EmptyScheduleRejected
		}
		err := r.Register(CronSpec{
			Name:     "bad-" + expr,
			Schedule: expr,
			TaskName: "x",
		})
		if !errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("Register(%q): got %v, want ErrInvalidSchedule", expr, err)
		}
	}
}

// TestRegister_AcceptsCommonExpressions ensures the parser accepts
// every expression shape the §8.2 catalog needs (standard 5-field,
// @daily/@hourly/@weekly descriptors, @every <duration> shorthand).
func TestRegister_AcceptsCommonExpressions(t *testing.T) {
	t.Parallel()
	good := []string{
		"0 3 * * *",    // 03:00 daily — revisions.purge
		"@hourly",      // autosave.purge / auth.session.cleanup
		"@daily",       // shorthand for "0 0 * * *"
		"0 4 * * 0",    // Sunday 04:00 — media.cleanup.cold_variants
		"*/5 * * * *",  // rum.aggregate
		"*/10 * * * *", // webhook.retry.dlq.scan
		"@every 1m",    // miniredis test cadence
		"@every 500ms", // sub-second test cadence
		"@every 250ms", // even tighter for race tests
	}
	r := NewRegistry()
	for i, expr := range good {
		err := r.Register(CronSpec{
			Name:     fmtName(i),
			Schedule: expr,
			TaskName: "demo.task",
		})
		if err != nil {
			t.Errorf("Register(%q): unexpected error: %v", expr, err)
		}
	}
}

func fmtName(i int) string {
	return "test.entry." + string(rune('a'+i))
}

// TestRegister_DuplicateErrors covers the first-writer-wins contract.
func TestRegister_DuplicateErrors(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	first := CronSpec{Name: "dup", Schedule: "@daily", TaskName: "task.a"}
	second := CronSpec{Name: "dup", Schedule: "@hourly", TaskName: "task.b"}

	if err := r.Register(first); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(second)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("second Register: got %v, want ErrAlreadyRegistered", err)
	}
	got, ok := r.Get("dup")
	if !ok {
		t.Fatal("Get: expected spec to still be present")
	}
	if got.Schedule != "@daily" || got.TaskName != "task.a" {
		t.Errorf("Get: expected first writer to win, got %+v", got)
	}
}

// TestNames_SortedDeterministic verifies Names returns the registered
// names sorted lexicographically.
func TestNames_SortedDeterministic(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, n := range []string{"c.daily", "a.daily", "b.daily"} {
		if err := r.Register(CronSpec{Name: n, Schedule: "@daily", TaskName: "x"}); err != nil {
			t.Fatalf("Register(%q): %v", n, err)
		}
	}
	got := r.Names()
	want := []string{"a.daily", "b.daily", "c.daily"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Names: got %v, want %v", got, want)
	}
}

// TestHas covers the membership convenience.
func TestHas(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if r.Has("nope") {
		t.Fatal("Has on empty registry: want false")
	}
	_ = r.Register(CronSpec{Name: "yes", Schedule: "@daily", TaskName: "x"})
	if !r.Has("yes") {
		t.Fatal("Has after Register: want true")
	}
}

// TestRegistry_ConcurrentRegister exercises the mutex with parallel
// Register calls. The race detector catches any unsynchronized map
// access. We don't assert on the surviving spec (any one of the
// N writers can win; the contract is "no race", not "deterministic
// ordering").
func TestRegistry_ConcurrentRegister(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			_ = r.Register(CronSpec{
				Name:     fmtName(i),
				Schedule: "@daily",
				TaskName: "x",
			})
		}()
	}
	wg.Wait()
	if got := len(r.Names()); got != n {
		t.Fatalf("Names: got %d, want %d", got, n)
	}
}
