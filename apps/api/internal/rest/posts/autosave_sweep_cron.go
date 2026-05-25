package posts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Singleton-Solution/GoNext/packages/go/jobs/cron"
	"github.com/Singleton-Solution/GoNext/packages/go/jobs/taskspec"
)

// AutosaveSweepTaskName is the taskspec name used by the daily TTL
// sweep of post_autosaves. Lives at the top of the file so call sites
// (registrations, worker dispatch, admin UI labels) reference the
// constant rather than a stringly-typed literal.
const AutosaveSweepTaskName = "posts.autosave.sweep"

// AutosaveSweepScheduleName is the cron schedule identifier for the
// TTL sweep. Convention is "<resource>.<action>.<cadence>" per
// packages/go/jobs/cron.CronSpec.Name's docstring.
const AutosaveSweepScheduleName = "posts.autosave.sweep.daily"

// AutosaveSweepSchedule is the cron expression that fires the sweep.
// 04:00 UTC daily — off-peak for nearly every region we ship in, and
// well outside the "European editorial morning" window where the
// admin sees the most autosave traffic. Encoded as a 5-field cron
// expression (Minute Hour DayOfMonth Month DayOfWeek).
const AutosaveSweepSchedule = "0 4 * * *"

// AutosaveSweepTTL is the age threshold used by Sweep. Rows whose
// updated_at is older than now() - AutosaveSweepTTL are deleted on
// each run. 7 days matches the contract documented in migration
// 000016 and the editor's "if you haven't touched this in a week we
// throw the unsaved draft away" UX.
const AutosaveSweepTTL = 7 * 24 * time.Hour

// AutosaveSweeper is the minimal surface the cron job needs.
// PgxAutosaveStore implements it. We narrow the interface here so
// tests can substitute a fake without spinning up Postgres.
type AutosaveSweeper interface {
	Sweep(ctx context.Context, olderThan time.Time) (int64, error)
}

// RegisterAutosaveSweep wires the TTL sweep into the project-wide
// taskspec + cron registries. The cron scheduler running on the
// worker picks both up at boot:
//
//   - The TaskSpec defines what to run (Sweep called with a 7-day
//     threshold) and on which queue (the default operational queue).
//   - The CronSpec defines when to run it (04:00 UTC daily).
//
// Both registrations are first-writer-wins (the underlying registries
// reject duplicates with ErrAlreadyRegistered). Calling
// RegisterAutosaveSweep twice on the same registry returns the
// duplicate error rather than overwriting — the prompt of "idempotent
// boot" is preserved because the second writer sees the same spec
// already exists.
//
// sweeper must not be nil. Pass the production PgxAutosaveStore in
// the binary's main(); tests can inject a fake AutosaveSweeper.
//
// logger may be nil; it falls back to slog.Default at fire time.
//
// taskReg / cronReg are the registries to write into. Production
// passes taskspec.Default() / a binary-owned cron.NewRegistry();
// tests construct fresh registries to keep the global state untouched.
func RegisterAutosaveSweep(
	sweeper AutosaveSweeper,
	taskReg *taskspec.Registry,
	cronReg *cron.Registry,
	logger *slog.Logger,
) error {
	if sweeper == nil {
		return errors.New("posts.RegisterAutosaveSweep: sweeper is required")
	}
	if taskReg == nil {
		return errors.New("posts.RegisterAutosaveSweep: taskReg is required")
	}
	if cronReg == nil {
		return errors.New("posts.RegisterAutosaveSweep: cronReg is required")
	}
	log := logger
	if log == nil {
		log = slog.Default()
	}

	// Handler closes over the sweeper. The payload is ignored — the
	// task knows everything it needs from constants (7-day TTL, the
	// store handle in the closure). A nil payload is the canonical
	// shape for cron-fired operational tasks; the schema accepts null.
	spec := taskspec.TaskSpec{
		Name:     AutosaveSweepTaskName,
		Queue:    "default",
		MaxRetry: 0,
		Timeout:  5 * time.Minute,
		Handler: func(ctx context.Context, _ []byte) error {
			threshold := time.Now().UTC().Add(-AutosaveSweepTTL)
			n, err := sweeper.Sweep(ctx, threshold)
			if err != nil {
				return fmt.Errorf("posts.autosave.sweep: %w", err)
			}
			log.Info("posts.autosave.sweep: complete",
				slog.Int64("deleted", n),
				slog.Time("threshold", threshold),
			)
			return nil
		},
	}
	if err := taskReg.Register(spec); err != nil {
		return fmt.Errorf("posts.RegisterAutosaveSweep: taskspec: %w", err)
	}
	if err := cronReg.Register(cron.CronSpec{
		Name:     AutosaveSweepScheduleName,
		Schedule: AutosaveSweepSchedule,
		TaskName: AutosaveSweepTaskName,
		// nil Payload — the schedule fires the spec's handler, which
		// reads the threshold from a constant and doesn't need any
		// per-fire input.
	}); err != nil {
		return fmt.Errorf("posts.RegisterAutosaveSweep: cron: %w", err)
	}
	return nil
}
