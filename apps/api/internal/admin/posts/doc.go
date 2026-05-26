// Package posts is the admin-side REST surface for post operations
// that don't belong on the public /api/v1/posts mount.
//
// Today it ships the revision browse + restore endpoints (issue #127):
// list a post's stored revisions and roll the post back to one of
// them. The restore writes a fresh "manual" revision (carrying the
// "Restored from revision X" comment, per docs/01-core-cms.md §4.4) so
// the audit trail stays linear — restores never overwrite history,
// they extend it.
//
// The mount path is /api/v1/admin/posts; routes are policy-gated by
// edit_posts (or edit_others_posts when the post is owned by someone
// else, but that distinction lives downstream — this layer enforces
// only the cheap presence check and leaves the meta-cap mapping to a
// future cut).
package posts
