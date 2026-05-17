// Package migrate implements the `gonext migrate` subcommand tree.
//
// Subcommands:
//
//   - up         — apply every pending up migration
//   - down [N]   — roll back N migrations (default 1; pass 0 to roll back ALL)
//   - status     — print the current version + dirty flag
//
// The subtree is a thin shell over packages/go/migrate, which owns the
// actual golang-migrate plumbing and the Postgres advisory lock. This
// package's only job is argument parsing, exit codes, and wiring stdout/
// stderr for tests.
//
// Exit codes:
//
//	0  success
//	1  migration error
//	2  usage error (bad flag, missing arg, unknown subcommand)
//
// See docs/05-admin-api.md §3.9 (CLI surface) and issue #96.
package migrate
