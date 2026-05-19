// Package site_editor is the backend for the "Site Editor Lite"
// admin surface — the operator-facing UI that lets a non-developer
// rewrite the active theme's template parts (header, footer, sidebar,
// uncategorised) without dropping into the theme package on disk.
//
// # Scope
//
// The lite cut covers template PARTS only. Full templates
// (index.html, single.html, archive.html, …) are deliberately out of
// scope and reserved for v0.2 (issue #439). Parts are the smaller,
// safer surface to ship first: they have a fixed, theme-declared
// inventory (theme.json#templateParts), they don't carry routing
// concerns, and a broken part degrades to "the site has no header"
// rather than "the site doesn't render at all".
//
// # The flow
//
// On the read path:
//
//  1. The admin UI calls GET /api/v1/admin/site_editor/parts. The
//     handler enumerates the active theme's TemplateParts, reads each
//     part's on-disk HTML, parses it into a BlockTree via the shared
//     html2blocks converter (#361), then consults the override store
//     for any operator-saved overrides. The override wins when present.
//
//  2. The result is a slice of Part values — one per declared part —
//     each carrying its name, title, area, the resolved BlockTree, and
//     a flag indicating whether the tree came from disk or from an
//     override (so the UI can offer a "Reset to theme default" button).
//
// On the write path:
//
//  1. The admin UI calls PUT /api/v1/admin/site_editor/parts/{name}
//     with a BlockTree JSON body. The handler validates the tree (each
//     block name must resolve to a registered block) and persists it
//     via the OverrideStore.
//
//  2. DELETE /api/v1/admin/site_editor/parts/{name} removes the
//     override, falling back to the on-disk default on the next read.
//
// # The capability
//
// Every endpoint is gated by policy.CapThemeEditParts ("theme.edit_parts").
// The capability is intentionally separate from CapEditThemes: an
// operator may be trusted to re-skin a header without being trusted
// to rewrite the theme package on disk (which would let them swap
// in arbitrary CSS / JS).
//
// # Storage contract
//
// Overrides live in the options table at the key
//
//	theme_mods.{theme}.parts.{name}
//
// — alongside the rest of the per-theme modifications (#325 settings
// registry pattern). The key shape is part of the package's contract;
// see the OverrideStore interface for the precise read/write surface
// the renderer plugs into.
//
// # Renderer integration
//
// The public renderer (when it lands as packages/go/theme/render) is
// expected to call site_editor.Resolve(ctx, theme, name) before
// reaching for the on-disk part — Resolve consults the override store
// first and falls back to the disk content. The current cut exposes
// the Resolve helper so future renderer wiring is a single function
// call; the renderer itself is reserved for issue #440.
package site_editor
