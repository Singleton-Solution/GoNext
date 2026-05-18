package cron

import (
	"fmt"
)

// TaskNameRevisionsPurge is the taskspec.TaskSpec.Name the bundled
// revisions-purge cron entry fires against. The taskspec definition
// itself lives in packages/go/revisions (where the handler is); this
// constant is the contract between cron and that spec. Mismatched
// names surface at fire time as a "task name not in registry" log
// line rather than a panic.
const TaskNameRevisionsPurge = "revisions.purge"

// ScheduleRevisionsPurgeDaily is the canonical schedule entry for the
// nightly revisions sweep introduced in #351. We give it a name keyed
// by the cadence so a future "revisions.purge.hourly" can coexist
// without colliding.
const ScheduleRevisionsPurgeDaily = "revisions.purge.daily"

// SeedDefaults registers the canonical bootstrap cron entries onto
// reg. Today there is one: a daily 03:00 sweep that enqueues the
// revisions pruner via the taskspec registry. As more periodic tasks
// land (idempotency-key prune, expired-session sweep, plugin health
// check, see docs/12-jobs-cron.md §8.2), they register here too.
//
// SeedDefaults is intentionally idempotent on duplicate calls: it
// returns ErrAlreadyRegistered (wrapped with the schedule name) if a
// caller seeds the same registry twice. The error is propagated as-is
// so test setups that compose seeds with custom entries see a useful
// signal.
//
// Returns nil if every entry registered successfully. The function is
// the right place to wire new canonical schedules so the worker
// binary's main.go stays a one-line call.
func SeedDefaults(reg *Registry) error {
	if reg == nil {
		return fmt.Errorf("cron: SeedDefaults: registry is required")
	}
	defaults := []CronSpec{
		{
			// 03:00 every day matches the §8.2 catalog. Running at
			// the same time across replicas isn't a concern because
			// leader election guarantees exactly one replica fires.
			// The 03:00 slot was chosen to land in the operational
			// quiet hour (post-midnight, pre-Europe-business-day).
			Name:     ScheduleRevisionsPurgeDaily,
			Schedule: "0 3 * * *",
			TaskName: TaskNameRevisionsPurge,
			// Payload is left nil: the pruner handler reads the
			// retention policy from process config, not from the
			// task payload. Schema-wise the revisions.purge taskspec
			// (in packages/go/revisions) is expected to accept
			// either an empty object or null, so a nil payload
			// passes validation.
		},
	}
	for _, spec := range defaults {
		if err := reg.Register(spec); err != nil {
			return fmt.Errorf("cron: SeedDefaults: %w", err)
		}
	}
	return nil
}
