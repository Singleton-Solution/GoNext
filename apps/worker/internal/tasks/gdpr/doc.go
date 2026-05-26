// Package gdpr implements the worker side of the GDPR data lifecycle
// (issue #216):
//
//   - gdpr.export.run   — fans out a single export job: queries every
//     table the user owns (profile, posts, comments,
//     media, audit rows), serialises each to JSON,
//     bundles into a ZIP, and uploads to the
//     configured object store. Surfaces the
//     download URL through the export-job status
//     row so the REST polling endpoint can serve it.
//
//   - gdpr.purge.tick   — cron-cadenced sweep. Runs every 10 minutes
//     (the schedule lives in the worker's main wiring,
//     not here, so operators can re-cadence without
//     code changes). For each user whose
//     scheduled_purge_at <= now(), runs the
//     hard-delete transaction: DELETE FROM users
//     CASCADE plus an explicit DELETE on the few
//     tables that don't cascade.
//
// The tasks deliberately live in apps/worker/internal rather than a
// shared package: the work is worker-local (it runs inside the asynq
// process), the SQL is large enough to warrant its own file tree, and
// nothing else in the repo needs to call these handlers directly.
package gdpr
