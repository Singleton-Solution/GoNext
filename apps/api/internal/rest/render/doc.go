// Package render hosts the HTTP surface that drives the Go
// server-side block walker (packages/go/blocks/render) for the
// editor's preview pane.
//
// Endpoint (mounted under /api/v1):
//
//	POST /api/v1/render/preview — accept a block tree + optional
//	                              context map, return rendered HTML
//	                              plus the per-block error list the
//	                              walker collected.
//
// The endpoint is intentionally narrow:
//
//   - Stateless: it consults only the registry held by the handler;
//     no DB round-trip, no auth requirement beyond what the admin
//     middleware applies upstream.
//   - One-shot: a single request renders one tree. The editor's
//     autosave loop drives many of these in flight; the renderer
//     is cheap (no I/O) so we don't shard or queue.
//   - Honest about errors: an unknown block type is non-fatal —
//     the response carries the placeholder HTML and the per-block
//     error list so the preview pane can surface a banner without
//     blanking out the canvas.
//
// The handler is mounted by apps/api/cmd/server/main.go alongside
// the other REST surfaces.
package render
