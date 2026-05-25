package posts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Singleton-Solution/GoNext/apps/api/internal/rest/posts"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

// fakeSweeper records the arguments handed to Sweep so the tests can
// assert "the cron handler invokes the sweeper with a roughly-7-day-
// old threshold". The store interface here is intentionally tiny —
// the unit test for the cron wiring doesn't care about Postgres.
type fakeSweeper struct {
	called    int
	lastOlder time.Time
	err       error
}

func (f *fakeSweeper) Sweep(_ context.Context, olderThan time.Time) (int64, error) {
	f.called++
	f.lastOlder = olderThan
	return 0, f.err
}

func TestRegisterAutosaveSweep_RegistersBoth(t *testing.T) {
	t.Parallel()
	taskReg := taskspec.NewRegistry()
	cronReg := cron.NewRegistry()

	sweeper := &fakeSweeper{}
	if err := posts.RegisterAutosaveSweep(sweeper, taskReg, cronReg, nil); err != nil {
		t.Fatalf("RegisterAutosaveSweep: %v", err)
	}

	// taskspec side: the named task exists.
	spec, ok := taskReg.Get(posts.AutosaveSweepTaskName)
	if !ok {
		t.Fatalf("task %q not registered", posts.AutosaveSweepTaskName)
	}
	if spec.Handler == nil {
		t.Errorf("task spec has nil Handler")
	}

	// cron side: the named schedule exists and points at the task.
	csp, ok := cronReg.Get(posts.AutosaveSweepScheduleName)
	if !ok {
		t.Fatalf("schedule %q not registered", posts.AutosaveSweepScheduleName)
	}
	if csp.TaskName != posts.AutosaveSweepTaskName {
		t.Errorf("schedule.TaskName = %q, want %q", csp.TaskName, posts.AutosaveSweepTaskName)
	}
	if csp.Schedule != posts.AutosaveSweepSchedule {
		t.Errorf("schedule.Schedule = %q, want %q", csp.Schedule, posts.AutosaveSweepSchedule)
	}
}

func TestRegisterAutosaveSweep_HandlerInvokesSweeperWith7DayThreshold(t *testing.T) {
	t.Parallel()
	taskReg := taskspec.NewRegistry()
	cronReg := cron.NewRegistry()

	sweeper := &fakeSweeper{}
	if err := posts.RegisterAutosaveSweep(sweeper, taskReg, cronReg, nil); err != nil {
		t.Fatalf("RegisterAutosaveSweep: %v", err)
	}

	spec, _ := taskReg.Get(posts.AutosaveSweepTaskName)

	before := time.Now().UTC()
	if err := spec.Handler(context.Background(), nil); err != nil {
		t.Fatalf("handler: %v", err)
	}
	after := time.Now().UTC()

	if sweeper.called != 1 {
		t.Fatalf("Sweep called %d times, want 1", sweeper.called)
	}
	// The threshold must be (now - 7d), bounded by the before/after
	// timestamps we sampled around the call. We accept a small skew
	// because the handler reads time.Now itself.
	wantLo := before.Add(-posts.AutosaveSweepTTL).Add(-time.Second)
	wantHi := after.Add(-posts.AutosaveSweepTTL).Add(time.Second)
	if sweeper.lastOlder.Before(wantLo) || sweeper.lastOlder.After(wantHi) {
		t.Errorf("threshold = %s, want within [%s, %s]",
			sweeper.lastOlder, wantLo, wantHi)
	}
}

func TestRegisterAutosaveSweep_HandlerPropagatesSweeperError(t *testing.T) {
	t.Parallel()
	taskReg := taskspec.NewRegistry()
	cronReg := cron.NewRegistry()

	wantErr := errors.New("boom")
	sweeper := &fakeSweeper{err: wantErr}
	if err := posts.RegisterAutosaveSweep(sweeper, taskReg, cronReg, nil); err != nil {
		t.Fatalf("RegisterAutosaveSweep: %v", err)
	}

	spec, _ := taskReg.Get(posts.AutosaveSweepTaskName)
	err := spec.Handler(context.Background(), nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("handler err = %v, want chain containing %v", err, wantErr)
	}
}

func TestRegisterAutosaveSweep_NilArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		sweeper posts.AutosaveSweeper
		taskReg *taskspec.Registry
		cronReg *cron.Registry
	}{
		{"nil sweeper", nil, taskspec.NewRegistry(), cron.NewRegistry()},
		{"nil taskReg", &fakeSweeper{}, nil, cron.NewRegistry()},
		{"nil cronReg", &fakeSweeper{}, taskspec.NewRegistry(), nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := posts.RegisterAutosaveSweep(tc.sweeper, tc.taskReg, tc.cronReg, nil); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}
