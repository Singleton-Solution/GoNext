// Package theme parses and validates GoNext theme.json v1 manifests and
// emits the derived CSS custom properties the public-site renderer
// injects into every page.
//
// This package implements the Go side of the "theme.json v1 schema and
// validator" surface described in docs/03-theme-system.md §3. It is the
// authority called by:
//
//   - The admin theme installer when a new theme package lands on disk
//     (validate before activation).
//   - The CI lint that runs on every theme PR in the registry.
//   - The `gonext theme validate` CLI subcommand.
//   - The renderer, which calls EmitCSSCustomProperties() to derive the
//     ":root { --gn-color-…: … }" block from the manifest.
//
// What's here:
//
//   - ThemeJSON, the typed manifest. Every key in the §3.1 example has a
//     Go field. Unknown top-level keys are rejected at parse time
//     (DisallowUnknownFields), which catches typos and accidental schema
//     drift before the renderer sees them.
//
//   - ColorEntry, FontFamily, FontFace, FontSize, SpacingScale,
//     TemplateDef, TemplatePartDef — the leaf value types. Their slugs
//     are constrained to kebab-case lowercase ASCII; colors and lengths
//     are validated against a permissive but well-defined CSS subset.
//
//   - Parse(data []byte) (*ThemeJSON, error): strict JSON parse +
//     structural validation. Returns a single error covering the parse
//     failure if the document isn't well-formed JSON or contains
//     unknown keys.
//
//   - (*ThemeJSON).Validate() []ValidationError: returns every error
//     in the document at once, with JSON-pointer-style paths (e.g.
//     "/settings/color/palette/0/color"). The caller can render the
//     full list to the admin or to stderr instead of fixing one error
//     at a time.
//
//   - (*ThemeJSON).EmitCSSCustomProperties() string: generates the
//     CSS custom-property block from the manifest. The output is
//     deterministic — palette entries are emitted in declaration
//     order — so it is safe to embed in cached HTML.
//
// Versioning: only v1 is recognised today. Future revisions will live in
// sibling packages (theme/v2, ...) per the §3.3 design note that
// drops WP's version=2 legacy and starts fresh at 1.
//
// House rule: nothing in this package depends on net/http, database/sql,
// or any GoNext subsystem package. Validation is a pure function over
// bytes so plugins, CLI tools, and tests can call it without dragging
// in the full server.
//
// See docs/03-theme-system.md §3 for the schema reference.
package theme
