// Package data implements the GDPR "right to access" and "right to
// erasure" endpoints (issue #216):
//
//	GET  /api/v1/account/data/export — returns a job id; the worker
//	                                   prepares a ZIP of the user's data
//	                                   (profile, posts, comments, media,
//	                                   audit-log rows) and uploads it to
//	                                   the configured object store.
//	POST /api/v1/account/data/delete — anonymises the user in-place and
//	                                   schedules a hard-delete 30 days
//	                                   out. Requires the current password
//	                                   in the request body to defeat CSRF
//	                                   and "I clicked the wrong button"
//	                                   accidents.
//
// Why these live in a dedicated package rather than under auth/account:
// the surface is small but the audit posture is loud — every call must
// emit a distinct audit event and the delete handler runs a multi-row
// UPDATE under a transaction. Keeping the code isolated keeps the
// blast radius of a refactor small.
//
// Rate-limit policy:
//   - export: 1 request per UTC day per user. Two reasons: the resulting
//     ZIP is expensive (touches every table that holds the user's
//     content) and a flood of exports is a recognised account-takeover
//     signal — an attacker who hijacks a session and then immediately
//     starts pulling data should hit a wall.
//   - delete: 5 requests per hour per user. The endpoint requires the
//     current password every time, so the bottleneck is bcrypt rather
//     than the rate limiter; we keep the rate limit anyway to absorb
//     credential-stuffing scripts that happen to hit this URL.
package data
