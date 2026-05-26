// Package scheduler implements the content-lifecycle background jobs:
//
//   - the scheduled-publisher cron task that flips eligible
//     status='scheduled' posts to status='published' (issue #143
//     state-machine half), and
//   - the trash-GC cron task that hard-deletes posts whose status
//     has been 'trash' for longer than the configured retention
//     window (default 30 days).
//
// Both tasks share a taskspec.TaskSpec + cron.CronSpec pair so the
// worker binary wires them with a few lines in main.go: declare the
// specs, register them against the process-wide cron registry, and
// the cron-leader goroutine fires them on schedule.
//
// # State machine
//
// The post lifecycle (per migrations/000001_init.up.sql) is:
//
//	draft → pending → scheduled → published
//	                            ↘
//	                             private
//	                            ↘
//	                             trash → (GC)
//
// 'scheduled' is the transition this package automates. When a post
// is saved with status='scheduled' and a scheduled_for time, the API
// stores it as-is; the publisher cron picks it up the first minute
// after scheduled_for elapses and flips status to 'published' and
// published_at to now(). The transition is a single UPDATE with a
// version-bumping trigger, so a concurrent editor's optimistic-
// concurrency check (WHERE version = …) still catches a race.
//
// # Retention / GC
//
// Trash is a soft delete: the row sticks around with status='trash'
// so an admin can restore it. After a configurable window (default
// 30 days; matches docs/01-core-cms.md §6) the GC task hard-deletes
// the row plus its derived rows (revisions, autosaves, etc., which
// have ON DELETE CASCADE FKs). The window starts at the most recent
// updated_at — moving to trash is itself an UPDATE that touches the
// timestamp, so a re-trashed-then-restored post effectively resets
// the clock.
//
// # Why not inline in the API
//
// Both tasks are bulk operations that pin a connection while they
// run — even a 100-row batch is multi-second on a busy primary. The
// API container's request-scoped budgets aren't built for that;
// running them on the worker container with its 240s drain budget
// matches every other batch task (revisions.purge, abort_orphans).
//
// # Cadence
//
// The scheduled-publisher runs every minute (the smallest cadence
// the project's cron parser accepts, see jobs/cron). Going faster
// is technically possible with @every but provides no real-world
// win: editors who set a scheduled_for time inside the next minute
// already accept that as part of the "scheduled publish" UX.
//
// The GC runs daily at 03:30 UTC — half an hour after the existing
// revisions.purge sweep so the two heavy queries don't pile up at
// the same minute.
package scheduler
