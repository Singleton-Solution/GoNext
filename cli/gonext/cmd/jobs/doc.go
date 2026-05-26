// Package jobs is the `gonext jobs ...` CLI subtree. It surfaces the
// background-job system (asynq + the cron scheduler) to operators
// running the CLI in a deploy. Each subcommand maps onto a question
// an operator typically asks at incident time:
//
//	gonext jobs queue   — "what's the queue depth right now?"
//	gonext jobs failed  — "what tasks are failing and why?"
//	gonext jobs drain   — "drain the DLQ (after a fix is deployed)"
//	gonext jobs cron    — "what cron schedules are registered?"
//	gonext jobs plugin  — "how much work is each plugin owning?"
//
// All subcommands talk to the same Redis instance as the apps/worker
// runtime — REDIS_URL is the canonical env var. The cron + plugin
// subcommands additionally read DATABASE_URL because the cron
// schedule registry + plugin task counters live in Postgres.
package jobs
