// Package templates implements the WordPress-style template hierarchy
// resolver described in docs/03-theme-system.md §4.
//
// The hierarchy is the heart of the theme system: given a parsed HTTP
// request, the resolver walks an ordered list of template filenames
// (most-specific to least-specific) and returns the first one the
// active theme actually ships. The list always terminates at
// "index.tsx" — themes that omit it are malformed.
//
// This package is intentionally narrow:
//
//   - Request describes a resolved query (singular post, taxonomy
//     archive, author archive, …). The router is responsible for
//     mapping URL → Request; the resolver does not touch net/http.
//
//   - RequestType enumerates the nine query buckets the docs name.
//     Each bucket has its own canonical precedence list in §4.2.
//
//   - ThemeFiles is the only filesystem abstraction. It exposes a
//     single Has(name) method so production callers can plug in a real
//     directory scan, tests can plug in an in-memory map, and an
//     eventual block-theme backend can plug in a database lookup —
//     all without changing this package.
//
//   - Resolver.Resolve(req, files) returns the chosen filename or an
//     error if even index.tsx is missing.
//
//   - DefaultResolver is the stock implementation that walks the §4.2
//     hierarchy. It tries each candidate first with the ".tsx"
//     extension (block themes / Next.js App Router themes) then
//     ".html" (classic static themes).
//
// House rule: this package is pure. It imports nothing from net/http,
// database/sql, os, or any GoNext subsystem package. That keeps it
// callable from the renderer, from CLI tools, from unit tests, and
// from plugin code that wants to introspect the precedence list
// without booting a server.
//
// See docs/03-theme-system.md §4 for the full reference and rationale.
package templates
