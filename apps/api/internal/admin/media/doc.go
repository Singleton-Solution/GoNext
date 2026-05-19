// Package media implements the operator-facing Media Library admin
// REST surface. It is the upload/list/edit/delete API that the admin UI
// at apps/admin/src/app/media/* talks to.
//
// Surface (all under /api/v1/admin/media, all capability-gated):
//
//	POST   /api/v1/admin/media            — multipart upload (≤ 50 MiB)
//	GET    /api/v1/admin/media            — paginated grid (type filter)
//	GET    /api/v1/admin/media/{id}       — single-asset detail
//	PATCH  /api/v1/admin/media/{id}       — edit alt_text + caption only
//	DELETE /api/v1/admin/media/{id}       — soft-delete tombstone
//
// The wire surface is deliberately narrow: filename and storage_key are
// immutable, the only metadata fields the UI can change are alt_text
// and caption (the accessibility text and the displayed description).
// Renaming a file or moving it inside the bucket would invalidate every
// rendered URL referring to it; that lives in a future bulk-operation
// surface, not the per-row PATCH.
//
// Storage layout
//
// The bytes live in S3 (or any S3-compatible store wired via
// config.StorageConfig). The handler computes a SHA-256 streaming
// during the multipart read, then probes the media table by hash: if a
// row with the same bytes already exists, we return the existing record
// without re-uploading to S3. This is the dedupe step the spec
// requires; it also defends against an operator racing two browser
// tabs onto the same logo.
//
// Soft delete + purge
//
// DELETE sets deleted_at on the row but leaves the S3 object in place.
// A nightly purge cron (lives in apps/worker, not in scope here) sweeps
// rows where deleted_at < now() - retention and removes both the row
// and the object. This split lets an operator undo a misclick by
// clearing deleted_at; if we deleted the bytes immediately, an undo
// would surface a 404 on every variant URL until the source was
// re-uploaded.
//
// Capability gates
//
//   - POST   → policy.CapMediaUpload
//   - PATCH  → policy.CapMediaUpload (same operator class)
//   - GET    → policy.CapMediaRead
//   - DELETE → policy.CapMediaDelete
//
// The split lets a constrained operator role (e.g. "media moderator")
// be granted read + delete without also getting upload; see the
// CapMediaUpload doc block in packages/go/policy/capabilities.go.
package media
