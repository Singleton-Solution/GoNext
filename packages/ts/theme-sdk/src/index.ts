/**
 * `@gonext/theme-sdk` — public entry point for theme authors.
 *
 * Themes import everything they need from this single package:
 *
 *  - **`theme.json` types** mirroring `packages/go/theme/types.go`
 *    (the Go side is the source of truth — see comments in
 *    `theme-json.ts` for the per-field mapping).
 *  - **Template-hierarchy helpers** mirroring the Go resolver in
 *    `packages/go/theme/templates/`. `templatePath()` returns the
 *    same name the host would resolve at request time.
 *  - **Block-tree types** re-exported from `@gonext/blocks-sdk` so
 *    theme code only imports one package.
 *  - **CSS custom-property helpers** (`cssVar()`) for referencing
 *    the renderer-emitted tokens documented in §3.2.
 *  - **`defineTheme()`** — the type-safe identity helper for
 *    authoring `theme.json` as a `.ts` file.
 *
 * Every symbol is a named export, no default — same convention the
 * blocks SDK uses; tree-shakers love it.
 */

// ─── theme.json types ────────────────────────────────────────────────
export type {
  BlockSettings,
  BlockStyle,
  BorderSettings,
  ColorBlock,
  ColorEntry,
  ColorSettings,
  DuotoneEntry,
  Element,
  FluidFontSize,
  FontFace,
  FontFamily,
  FontSize,
  GradientEntry,
  LayoutSettings,
  ShadowPreset,
  ShadowSettings,
  Settings,
  SpacingScale,
  SpacingSettings,
  StyleBorder,
  StyleColor,
  StyleTypography,
  Styles,
  Supports,
  TemplateDef,
  TemplatePartArea,
  TemplatePartDef,
  ThemeJson,
  ThemeJsonVersion,
  TypographySet,
} from './theme-json.ts';

export { THEME_JSON_CURRENT_VERSION } from './theme-json.ts';

// ─── Template hierarchy ──────────────────────────────────────────────
export type {
  ContextHints,
  RequestType,
  TemplateResolution,
} from './templates.ts';

export {
  TEMPLATE_EXTENSIONS,
  buildCandidates,
  templatePath,
} from './templates.ts';

// ─── Blocks (re-exported from @gonext/blocks-sdk) ────────────────────
export type {
  AttributesSchema,
  Block,
  BlockAttributes,
  BlockCategory,
  BlockDeprecation,
  BlockEditProps,
  BlockSaveProps,
  BlockSupports,
  BlockTree,
  BlockTypeDefinition,
  EditComponent,
  SaveComponent,
  ValidationError,
  ValidationResult,
} from './blocks.ts';

// ─── Styles / CSS helpers ────────────────────────────────────────────
export type { ThemeToken } from './styles.ts';
export { CSS_VAR_PREFIX, cssVar } from './styles.ts';

// ─── defineTheme ─────────────────────────────────────────────────────
export type { Exact, GoNextTheme } from './define-theme.ts';
export { defineTheme } from './define-theme.ts';
