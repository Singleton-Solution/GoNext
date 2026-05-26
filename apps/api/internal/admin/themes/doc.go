// Package themes implements the admin REST surface for the theme
// installer + switcher (issues #13, #18, #65).
//
// What's here:
//
//   - GET /api/v1/admin/themes
//     List installed themes (directories under ThemeDir whose
//     theme.json parses + validates). The response carries the slug
//     of the active theme so the switcher UI can render the "active"
//     badge without an extra round trip.
//
//   - POST /api/v1/admin/themes/install
//     Accept a multipart upload of a .gntheme ZIP, validate its
//     theme.json manifest, and extract into ThemeDir/<slug>/. The
//     installer is fail-closed: if the manifest is missing or fails
//     validation, the ZIP is rejected without writing a byte to disk.
//
//   - POST /api/v1/admin/themes/activate
//     Switch the core.active_theme option. The handler validates the
//     target slug exists on disk before writing the option so the
//     next page render can't 500 on a missing manifest.
//
// House rule: the package owns the filesystem-mutation logic for
// theme directories (extract, validate, write the active-slug
// option). The customizer package keeps its narrow read+overrides
// surface untouched.
package themes
