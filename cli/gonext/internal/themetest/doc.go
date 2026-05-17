// Package themetest implements the structural checks for the
// `gonext theme test` contract runner described in docs/11-testing-ci.md §6.
//
// The full §6.1 contract has seven checks:
//
//  1. Manifest valid (theme.json schema)
//  2. Template hierarchy fallback resolves for every route class
//  3. theme.json semantics (palette / spacing / block-variation references)
//  4. Block style variations apply (renders a sample of each variation)
//  5. Accessibility scan (axe-core against canonical templates)
//  6. Bundle-size budgets per template
//  7. SSR parity (deterministic across two renders with identical inputs)
//
// Checks 4–7 depend on the theme runtime (React + Next.js renderer)
// which does not exist yet — see docs/03-theme-system.md §13. This package
// therefore implements the checks that are doable from on-disk inspection
// alone, and emits a deterministic SKIP row for each runtime-dependent check
// so the report shape is stable once the renderer lands.
//
// The doable checks today:
//
//   - theme.json: presence; valid JSON; required keys (version, optional
//     settings sub-objects: color/typography/spacing); references to
//     templateParts/customTemplates match files on disk.
//   - package.json: presence; valid JSON; "gonext" key with "kind":"theme";
//     "type" is "block" or "classic"; name follows @scope/theme-* or
//     @scope/<slug> convention recommended in docs/03-theme-system.md §2.
//   - Template hierarchy presence: at minimum templates/index.tsx (or
//     templates/index.json for block themes) must exist; canonical entries
//     (single, page, archive, 404, search) are reported as "found" or
//     "missing" (advisory, not failure) so authors can see what fallbacks
//     the runtime will exercise.
//   - parts/ inspection (optional): header.tsx and footer.tsx are reported
//     advisory; absent parts are not a failure but a NOTE.
//   - Block-vs-classic detection: declared "type" in package.json must
//     match the presence of templates/*.json (block) vs templates/*.tsx
//     (classic). Mixed templates are allowed but emit a NOTE.
//
// The runner outputs one row per check. With --json a structured Report is
// emitted instead. Exit code is 0 iff every Status == StatusPass or
// StatusSkip; any StatusFail yields exit 1.
package themetest
