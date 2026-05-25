// Package collections is the data layer for media folders.
//
// A "collection" is a hierarchical folder that media assets can be
// filed into. The hierarchy is materialised two ways:
//
//   - parent_id self-reference (an adjacency list).
//   - path column of type ltree (a materialised path).
//
// Adjacency makes moves cheap (one column update); ltree makes
// descendant/ancestor queries cheap (index scan). The two MUST stay
// in sync — every mutation that changes parent_id also rewrites the
// affected path values. The Store implementations in this package
// hold that invariant.
//
// The interface is intentionally small:
//
//   - Create: append a child under a parent (or as a root).
//   - Get / List: by id, by path, or all descendants of a node.
//   - Rename: change a node's name (cheap, only the leaf segment of
//     path changes — for every descendant).
//   - Move: change a node's parent. The path of every descendant is
//     rewritten in one UPDATE.
//   - Delete: remove a node + every descendant (cascade).
//
// Two backends:
//
//   - MemoryStore: pure Go, used by tests and by the single-binary
//     admin smoke runner.
//   - (Postgres-backed Store lands alongside the rest of the data-
//     access layer in a follow-up wiring change. The wire surface is
//     identical.)
//
// The package does not own the media row itself — that's
// apps/api/internal/admin/media. The two communicate through the
// collection_id foreign key on the media row.
package collections
