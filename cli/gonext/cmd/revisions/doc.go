// Package revisions implements the `gonext revisions` subcommand tree.
//
// Subcommands:
//
//   - prune  — sweep the post_revisions table per the retention policy
//
// The package is a thin shell over packages/go/revisions/Pruner. Its
// only job is flag parsing, exit codes, and wiring stdout/stderr for
// tests. The retention rules and the FOR UPDATE SKIP LOCKED contract
// live in the Pruner.
//
// Exit codes:
//
//	0  success
//	1  prune failure (DB error, etc.)
//	2  usage error (bad flag, missing arg, unknown subcommand)
//
// See docs/01-core-cms.md §4.3 (retention semantics) and issue #169.
package revisions
