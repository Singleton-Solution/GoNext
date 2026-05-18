// Package status implements the operator-facing System Status surface.
//
// The /healthz and /readyz endpoints exist for orchestrator probes
// (Kubernetes liveness/readiness) — they answer "is this pod healthy"
// in two well-defined response shapes that monitoring systems consume.
// This package answers a different question: "as a human operator on
// the admin app, what does the system look like RIGHT NOW?"
//
// The aggregated report covers eight surfaces:
//
//   - build info  — version, commit, build date, Go version, GOOS/GOARCH.
//   - database    — pgxpool.Pool stats + Postgres server_version + ping RTT.
//   - redis       — INFO-derived redis_version + ping RTT.
//   - migrations  — current schema_migrations version, dirty flag, total
//     count of bundled .up.sql files.
//   - queues      — for each of the seven Asynq queues, pending/active
//     plus 24h processed/failed counters off the Inspector.
//   - theme       — active theme name+version + the count of declared
//     parts and customTemplates (parses theme.json on disk).
//   - plugins     — installed/active/errored counts off lifecycle.Storage
//     plus the most recent installed_at timestamp.
//   - disk        — bytes used by the theme directory and the media
//     directory (walks the trees; cheap on the order of seconds).
//
// # Authorization
//
// The handler sits behind policy.Require(p, policy.CapSystemRead). The
// capability is granted to admin and super_admin by default and is
// distinct from manage_install — System Status is a *read* surface, so
// a future "auditor" role (operator who can see the system but not
// mutate it) can hold system_read without inheriting destructive caps.
//
// # Wiring
//
// Sources are passed in as interfaces (Sources struct), not concrete
// types, so tests stub each axis independently without standing up
// Postgres / Redis / a real Asynq cluster. Production wiring lives in
// apps/api/cmd/server/main.go and threads the live *pgxpool.Pool,
// *redis.Client, *asynq.Inspector, *lifecycle.Storage, theme registry,
// and migration directory.
//
// # Stability
//
// The JSON shape is intentionally stable: the admin UI parses field
// names by hand (no shared codegen yet — that's #214's job), and the
// "Copy diagnostic" button on the page produces a redacted dump that
// support tickets quote verbatim. Adding fields is fine; renaming or
// removing fields breaks bookmarks and is a breaking change.
package status
