// Package comments hosts the public REST surface for comment display
// and submission.
//
// Endpoints (mounted under /api/v1/posts):
//
//	GET  /api/v1/posts/{id}/comments — list approved comments on a
//	                                    post, ordered by ltree path so
//	                                    threads render naturally.
//	                                    Pagination via cursor on
//	                                    (created_at, id).
//	POST /api/v1/posts/{id}/comments — submit a new comment on a post.
//	                                    Anonymous OR logged-in. The
//	                                    content is sanitised, the row
//	                                    runs through a spam check, and
//	                                    lands in 'pending' until a
//	                                    moderator (or auto-approval
//	                                    rule) flips it to 'approved'.
//
// The list endpoint is public (no auth required) but only returns
// rows in status 'approved' — pending / spam / trash bodies are the
// moderation surface's concern. The submit endpoint is rate-limited
// per source IP to defeat a trivial flood.
//
// CORS: both endpoints honour Config.PublicSite.BaseURL via a
// permissive Access-Control-Allow-Origin set on the response. The
// design intent is "apps/web (and only apps/web) calls us from the
// browser"; in practice we accept any same-origin or same-site call
// because the renderer is the only first-party browser surface and
// the read side is approved-comments-only.
//
// Threading model
// ===============
// Comments are stored with an `ltree` path materialised by a DB
// trigger (migration 000006). The submit handler relies on the
// trigger to compute path = parent.path || self.label. We never
// assemble an ltree literal in Go — we just insert with parent_id
// set and read back the row.
//
// Spam check
// ==========
// The spam check is a placeholder (issue #190 covers wiring the real
// scorer). Today we apply three trivial rules:
//
//   - Content longer than 5000 bytes after sanitisation → spam.
//   - More than five URLs in the body → spam.
//   - The author IP has submitted >10 comments in the last 5 minutes →
//     spam (rate-limit-as-spam-classifier).
//
// Anything else lands in 'pending'. Once the real scorer arrives, the
// public surface only swaps the spamCheck implementation; the handler
// shape doesn't change.
package comments
