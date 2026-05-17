/**
 * TypeScript types for the GoNext `theme.json` manifest.
 *
 * Every type in this file mirrors a Go struct in `packages/go/theme/types.go`.
 * The Go side is the source of truth — the parser, validator, and CSS
 * custom-property emitter all consume those structs, and what they accept
 * is what themes are allowed to ship. The mapping is one-to-one and the
 * comments below name the Go counterpart for each TypeScript type so a
 * reviewer (or `grep`) can confirm drift hasn't crept in.
 *
 * Mapping rules (kept consistent across every field):
 *
 *  - Go `json:"foo,omitempty"`        → TS `foo?: …`
 *  - Go `json:"foo"` (required tag)   → TS `foo: …`
 *  - Go `*T` pointer                  → TS `T | undefined` via `?:`
 *  - Go `map[string]T`                → TS `Record<string, T>`
 *  - Go `[]T`                         → TS `T[]`
 *
 * The names mirror the JSON wire shape (camelCase), not the Go field
 * identifiers (which are PascalCase). This is the same surface a theme
 * author edits when authoring `theme.json` by hand, so the type and the
 * file have identical shapes.
 *
 * See `docs/03-theme-system.md` §3.1 for the canonical example manifest
 * and §3.3 for the differences from WordPress's `theme.json`.
 */

/**
 * The only schema version this SDK accepts. Mirrors
 * `theme.CurrentVersion` (Go). The §3.3 design note explicitly drops
 * WordPress's `version: 2` legacy — GoNext starts fresh at `1`.
 */
export const THEME_JSON_CURRENT_VERSION = 1;

/**
 * Type-level alias for the literal `1` so `defineTheme()` can pin
 * `version` to it without callers having to write `1 as const`. If we
 * ever bump the schema, this is the one-line change.
 */
export type ThemeJsonVersion = typeof THEME_JSON_CURRENT_VERSION;

/**
 * The root manifest. Mirrors Go `theme.ThemeJSON`.
 *
 * Every key in §3.1 of `docs/03-theme-system.md` maps to one field
 * here; absent JSON keys leave the corresponding TS field undefined.
 * Validation rules live on the Go side — this surface is structural
 * only, intended to be enforced at *authoring* time by the compiler
 * (and at *install* time by the Go validator).
 */
export interface ThemeJson {
  /**
   * Optional JSON-Schema URL. When present, authoring tools (VS Code,
   * etc.) can offer autocomplete. Mirrors Go `Schema string \`json:"$schema,omitempty"\``.
   */
  $schema?: string;

  /**
   * Manifest schema version. Must equal `THEME_JSON_CURRENT_VERSION`.
   * Mirrors Go `Version int \`json:"version"\``.
   */
  version: ThemeJsonVersion;

  /**
   * Human-readable theme name; also surfaced in the admin switcher
   * beside the screenshot. Mirrors Go `Title string \`json:"title,omitempty"\``.
   */
  title?: string;

  /**
   * The design-token surface: palette, type scale, spacing, layout,
   * border, shadow presets, block opt-ins. Mirrors Go `Settings Settings \`json:"settings"\``.
   */
  settings: Settings;

  /**
   * The concrete style application — typically `var()` references back
   * into the tokens declared under `settings`. Mirrors Go
   * `Styles Styles \`json:"styles,omitempty"\``.
   */
  styles?: Styles;

  /**
   * Which optional renderer features the theme opts into. Unknown keys
   * are ignored on the Go side (forward-compatible). Mirrors Go
   * `Supports Supports \`json:"supports,omitempty"\``.
   */
  supports?: Supports;

  /** Block-pattern slugs the theme contributes to the editor. */
  patterns?: string[];

  /** Page-template aliases that show up in the admin "Template" dropdown. */
  customTemplates?: TemplateDef[];

  /** Named regions (header / footer / sidebar / uncategorized) this theme ships parts for. */
  templateParts?: TemplatePartDef[];
}

// ─── Settings ────────────────────────────────────────────────────────

/** Mirrors Go `theme.Settings`. */
export interface Settings {
  appearanceTools?: boolean;
  color?: ColorSettings;
  typography?: TypographySet;
  spacing?: SpacingSettings;
  layout?: LayoutSettings;
  border?: BorderSettings;
  shadow?: ShadowSettings;
  blocks?: Record<string, BlockSettings>;
}

/** Mirrors Go `theme.ColorSettings`. */
export interface ColorSettings {
  palette?: ColorEntry[];
  gradients?: GradientEntry[];
  custom?: boolean;
  customGradient?: boolean;
  defaultPalette?: boolean;
  duotone?: DuotoneEntry[];
}

/**
 * One named color in the palette. Mirrors Go `theme.ColorEntry`.
 *
 *  - `slug` is the stable machine identifier used as the CSS
 *    custom-property suffix.
 *  - `name` is the human label surfaced in the editor.
 *  - `color` is the CSS color value (hex, rgb(), hsl(), or a named
 *    CSS color).
 */
export interface ColorEntry {
  slug: string;
  name: string;
  color: string;
}

/** Mirrors Go `theme.GradientEntry`. */
export interface GradientEntry {
  slug: string;
  name: string;
  gradient: string;
}

/** Mirrors Go `theme.DuotoneEntry`. */
export interface DuotoneEntry {
  slug: string;
  name: string;
  colors: string[];
}

/** Mirrors Go `theme.TypographySet`. */
export interface TypographySet {
  fontFamilies?: FontFamily[];
  fontSizes?: FontSize[];
  lineHeight?: boolean;
  letterSpacing?: boolean;
  textDecoration?: boolean;
  custom?: boolean;
}

/** Mirrors Go `theme.FontFamily`. */
export interface FontFamily {
  slug: string;
  name: string;
  fontFamily: string;
  fontFace?: FontFace[];
}

/** Mirrors Go `theme.FontFace`. A single `@font-face` descriptor. */
export interface FontFace {
  src: string;
  fontWeight?: string;
  fontStyle?: string;
  fontDisplay?: string;
}

/**
 * Mirrors Go `theme.FontSize`. `size` is the default; `fluid`, when
 * set, declares the `clamp()` min/max for fluid scaling — the Go
 * emitter produces `clamp(min, size, max)`.
 */
export interface FontSize {
  slug: string;
  name: string;
  size: string;
  fluid?: FluidFontSize;
}

/** Mirrors Go `theme.FluidFontSize`. */
export interface FluidFontSize {
  min: string;
  max: string;
}

/** Mirrors Go `theme.SpacingSettings`. */
export interface SpacingSettings {
  units?: string[];
  spacingScale?: SpacingScale;
  padding?: boolean;
  margin?: boolean;
  blockGap?: boolean;
}

/**
 * Generated spacing scale. Mirrors Go `theme.SpacingScale`.
 *
 * The §3.3 design note names this as the only blessed way to declare
 * spacing — WordPress's "presets vs scale" duality is gone.
 */
export interface SpacingScale {
  /** Arithmetic operator: `"+"` or `"*"`. */
  operator?: '+' | '*';
  /** Step size. */
  increment?: number;
  /** How many steps. */
  steps?: number;
  /** Anchor value. */
  mediumStep?: number;
  /** Unit. Closed set on the Go side — see `validate.go`. */
  unit?: 'px' | 'rem' | 'em' | '%' | 'vw' | 'vh';
}

/**
 * Mirrors Go `theme.LayoutSettings`. The renderer emits these as
 * `--gn-layout-content` and `--gn-layout-wide` per §3.2.
 */
export interface LayoutSettings {
  contentSize?: string;
  wideSize?: string;
}

/** Mirrors Go `theme.BorderSettings`. */
export interface BorderSettings {
  color?: boolean;
  radius?: boolean;
  style?: boolean;
  width?: boolean;
}

/** Mirrors Go `theme.ShadowSettings`. */
export interface ShadowSettings {
  presets?: ShadowPreset[];
}

/** Mirrors Go `theme.ShadowPreset`. */
export interface ShadowPreset {
  slug: string;
  name: string;
  shadow: string;
}

/**
 * Per-block opt-in surface declared under `settings.blocks[name]`.
 * Mirrors Go `theme.Block`. The shape intentionally mirrors the
 * top-level `Settings` so themes can scope features to individual
 * block types — but the per-block surface is narrower (only border
 * and color today).
 */
export interface BlockSettings {
  border?: BorderSettings;
  color?: ColorBlock;
}

/** Mirrors Go `theme.ColorBlock`. */
export interface ColorBlock {
  background?: boolean;
  text?: boolean;
}

// ─── Styles ──────────────────────────────────────────────────────────

/** Mirrors Go `theme.Styles`. */
export interface Styles {
  color?: StyleColor;
  typography?: StyleTypography;
  elements?: Record<string, Element>;
  blocks?: Record<string, BlockStyle>;
}

/** Mirrors Go `theme.StyleColor`. */
export interface StyleColor {
  background?: string;
  text?: string;
}

/** Mirrors Go `theme.StyleTypography`. */
export interface StyleTypography {
  fontFamily?: string;
  fontSize?: string;
  lineHeight?: string;
}

/** Mirrors Go `theme.Element`. */
export interface Element {
  color?: StyleColor;
  typography?: StyleTypography;
}

/** Mirrors Go `theme.BlockStyle`. */
export interface BlockStyle {
  color?: StyleColor;
  border?: StyleBorder;
}

/** Mirrors Go `theme.StyleBorder`. */
export interface StyleBorder {
  radius?: string;
  color?: string;
  style?: string;
  width?: string;
}

// ─── Supports ────────────────────────────────────────────────────────

/** Mirrors Go `theme.Supports`. */
export interface Supports {
  blockTemplates?: boolean;
  siteEditor?: boolean;
  darkModeAuto?: boolean;
  customizer?: boolean;
  menus?: string[];
  widgetAreas?: string[];
}

// ─── Templates ───────────────────────────────────────────────────────

/**
 * Custom page template, e.g. the §3.1 example's `page-landing` for
 * the `"page"` post type. Mirrors Go `theme.TemplateDef`.
 */
export interface TemplateDef {
  name: string;
  title?: string;
  area?: string;
  postTypes?: string[];
}

/**
 * Named template part. Mirrors Go `theme.TemplatePartDef`. The `area`
 * field is one of the closed set `"header" | "footer" | "sidebar" |
 * "uncategorized"` — enforced by the Go validator.
 */
export interface TemplatePartDef {
  name: string;
  title?: string;
  area: TemplatePartArea;
}

/**
 * Closed set of template-part areas. Matches `validAreas` in
 * `packages/go/theme/validate.go` (the §5.3 list of logical regions).
 */
export type TemplatePartArea = 'header' | 'footer' | 'sidebar' | 'uncategorized';
