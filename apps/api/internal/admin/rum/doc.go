// Package rum implements the GoNext in-house Real User Monitoring
// (RUM) backend. Issue #132.
//
// # Responsibilities
//
// Two surfaces live in this package:
//
//   - Anonymous beacon ingest at POST /_/rum/beacon. The public theme's
//     RUM bundle (packages/ts/rum-beacon) emits batches of Core Web
//     Vitals + custom timings here. The endpoint is unauthenticated,
//     IP-rate-limited, payload-capped (16 KiB), batch-capped (50
//     events), and writes to the rum_events table introduced by
//     migration 000023.
//
//   - Authenticated aggregate read at GET /api/v1/admin/rum/percentiles.
//     The admin Performance page renders p50/p75/p95 over a sliding
//     window per metric and route. The handler is gated by
//     CapJobsAdmin (the closest "operator looking at infrastructure"
//     capability; future role refinements can route a dedicated
//     CapRUMRead through here without code changes).
//
// # Why first-party
//
// Operators want Core Web Vitals without sending data to a third party.
// The standard SaaS pitches (Datadog RUM, New Relic Browser, Sentry
// Performance) require shipping data outside the operator's trust
// boundary. By terminating beacons inside the GoNext API and rendering
// them in the GoNext admin, the operator gets the same visibility with
// none of the GDPR/SCC overhead.
//
// # PII posture
//
// session_id is a CLIENT-generated random token, hashed in the browser
// before transmission. The server stores the hash; it never learns the
// pre-hash identifier and never joins it against the users table. IPs
// are used only for rate-limit bucketing and optional country
// derivation; they are not stored. See docs/15-privacy-rum.md for the
// full posture statement.
//
// # Configuration
//
// The beacon endpoint is mounted unconditionally; the public theme
// only emits beacons when Config.RUM.Enabled is true. This means the
// table exists from migration time, but an off-by-default config
// keeps the data store empty until an operator opts in.
package rum
