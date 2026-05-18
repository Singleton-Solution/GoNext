// Package jobs implements the admin-facing DLQ (dead-letter queue)
// surface for archived background tasks.
//
// Issue #262 — DLQ admin UI.
//
// # Background
//
// Asynq archives tasks whose handlers exhaust their retry budget. The
// archived set is durable in Redis and is the canonical "things broke,
// look at me" queue. The chassis (#362) gives us the server side of the
// pipeline; the TaskSpec layer (#355) the contract; webhook delivery
// (#348) and idempotency (#363) are common sources of failure that end
// up here. Without an admin surface, operators have to shell into Redis
// to inspect the carnage, which is both slow and dangerous (any typo on
// the raw key is irreversible).
//
// This package is the HTTP surface that backs the admin UI's DLQ pages.
// It exposes:
//
//   - GET  /api/v1/admin/jobs/dlq          — list archived tasks
//   - GET  /api/v1/admin/jobs/dlq/{id}     — fetch a single archived task
//   - POST /api/v1/admin/jobs/dlq/{id}/replay   — push back onto the queue
//   - POST /api/v1/admin/jobs/dlq/{id}/discard  — permanently delete
//   - POST /api/v1/admin/jobs/dlq/{id}/redact   — mask sensitive fields
//
// # Authorization
//
// Every route requires the jobs.admin capability (see packages/go/policy/).
// Without it the handler returns 403 — even the list endpoint, because
// payload previews can disclose internal data.
//
// # Redaction
//
// Some archived tasks carry sensitive material in their payload (API
// tokens echoed by a third-party webhook, customer PII bundled in an
// envelope). Rather than scrub the Asynq payload in place (which would
// also break replay), we keep a small Postgres table — task_redactions
// — that records which fields to mask before display. The listing path
// reads the table on every render so a redaction takes effect
// immediately. Asynq's bytes on disk are untouched, so replay still
// hits the original payload.
//
// # Inspector
//
// We accept the Inspector interface (a subset of *asynq.Inspector) so
// tests can supply a fake without standing up Redis. Production wiring
// passes *asynq.Inspector directly; it satisfies the interface
// automatically.
package jobs
