// Package blocks exposes the admin REST surface for the block-model
// features: reusable (synced) blocks, post-type templates, and the
// "force block migration" pipeline. The routes are documented in
// docs/04-block-editor.md.
//
// Mount points (under /api/v1/admin):
//
//   /blocks/reusable          → reusable.Mount  (issue #193)
//
// Each sub-package mounts its own sub-tree and validates its own
// Deps bag. The top-level package only exists to anchor a single
// "admin/blocks" surface so the OpenAPI generator and the admin
// router walk it together.
package blocks
