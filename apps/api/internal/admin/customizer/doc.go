// Package customizer implements the admin Theme Customizer surface
// (issue #355). Operators read the active theme manifest plus the
// currently persisted overrides, edit values (palette, typography,
// layout, spacing) through the admin UI, and commit the resulting
// partial overrides without editing theme.json on disk.
//
// # Wire shape
//
//	GET  /api/v1/admin/customizer/active
//	PUT  /api/v1/admin/customizer/active
//
// Both endpoints are gated by the theme.customize capability.
//
// The GET response carries:
//
//   - theme         — the parsed manifest from the active theme's
//                     theme.json (defaults baked into the theme).
//   - themeSlug     — the slug of the active theme.
//   - overrides     — the partial-override JSON object currently in
//                     the options table; empty when none have been set.
//
// The PUT body is a partial-override JSON object. The handler validates
// the override by deep-merging it onto a zero ThemeJSON and running the
// theme/validate validator over the merged form. Any path the validator
// flags is rejected with 400; the override is persisted only if every
// path passes.
//
// # Storage
//
// Overrides live in the options table under the key
// "theme_mods.<theme-slug>". The slug is the value stored under
// "core.active_theme" by the theme seeder; the namespace prefix
// "theme_mods." matches the WordPress convention an operator coming
// from there expects.
//
// The options row is autoload=false because the renderer reads it on a
// per-request hot path via the settings store's L1 cache — autoloading
// would prime the cache on boot regardless of whether the request
// actually rendered the theme, wasting memory on API-only deployments.
//
// # Validation strategy
//
// Operators can only override known theme.json paths. The handler walks
// the override JSON, normalizes the structure into a fresh ThemeJSON
// value (rejecting unknown keys via DisallowUnknownFields), then merges
// it onto the active theme's manifest and runs theme.Validate on the
// merged form. The merged-then-validated approach has two virtues:
//
//  1. The same validator that protects theme.json on install protects
//     the overrides — there is one source of truth for "what's a valid
//     CSS color / slug / length".
//  2. Cross-field rules (e.g. fluid-font min/max) still work because
//     the override sees the manifest's other fields during validation.
package customizer
