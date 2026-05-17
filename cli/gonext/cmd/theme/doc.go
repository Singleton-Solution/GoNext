// Package theme implements the `gonext theme` subcommand tree.
//
// Today the only subcommand is `gonext theme test <dir>` — the contract
// runner specified in docs/11-testing-ci.md §6. Future subcommands
// (`install`, `activate`, `list`) live here too.
//
// The runner asserts the structural shape of a theme package. Renderer-
// dependent §6.1 checks (a11y, bundle size, SSR parity) are scaffolded
// as deterministic SKIP rows in the report — see
// internal/themetest/doc.go for the rationale.
package theme
