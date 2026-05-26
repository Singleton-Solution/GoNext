// Package media serves the public read-only `/api/v1/media` REST
// surface. The admin/media package owns the write surface (uploads,
// metadata edits, soft-delete); this one only exposes the read paths
// a public-facing site needs to render images.
//
// Endpoints:
//
//	GET  /api/v1/media         — list public assets (cursor pagination)
//	GET  /api/v1/media/{id}    — fetch a single asset's metadata + URLs
//
// The wire shape matches admin/media.Asset on the read side so a
// shared admin/public asset card can be rendered from either response.
// What's filtered out from the public surface:
//
//   - UploaderID — privacy. The author of an upload isn't part of the
//     asset's public identity (the post's author is what readers care
//     about; uploader is an audit field).
//   - SHA256 — internal dedupe key. Surfacing it would only be useful
//     to a CDN cache key forger.
//
// Variants and PublicURL ARE surfaced — they're what enables a
// responsive image renderer on the public site.
package media
