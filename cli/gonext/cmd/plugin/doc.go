// Package plugin implements the `gonext plugin` subcommand tree. It is a
// self-contained dispatcher: main.go delegates the entire `plugin ...`
// arg slice via [Run], and this package owns parsing, help, and exit codes
// for the subtree.
//
// Subcommands today:
//
//   - test — run the plugin contract checks against a bundle (directory or
//     `.gnplugin` archive). Returns exit 0 on all-pass, 1 on any-fail, 2 on
//     usage errors.
//
// Other subcommands listed in docs/02-plugin-system.md §11 (install,
// activate, list, dev, replay) are planned but not in this commit.
package plugin
