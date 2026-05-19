/**
 * Theme Customizer — wire shapes.
 *
 * Mirrors the Go types under `apps/api/internal/admin/customizer` and
 * `packages/go/theme`. We intentionally don't import a generated client
 * yet — the surface is small and the customizer page is the only
 * consumer, so a hand-rolled shape stays cheaper than the bundling
 * cost of dragging in the OpenAPI generator's runtime.
 */

/** Palette entry — one named color in the theme. */
export interface ColorEntry {
  slug: string;
  name: string;
  color: string;
}

/** Font family entry — one declaration in the type scale. */
export interface FontFamily {
  slug: string;
  name: string;
  fontFamily: string;
}

/** Font size entry — one step in the type scale. */
export interface FontSize {
  slug: string;
  name: string;
  size: string;
}

/** Layout content/wide size tokens. */
export interface LayoutSettings {
  contentSize?: string;
  wideSize?: string;
}

/** Spacing scale declaration (operator + steps style). */
export interface SpacingScale {
  operator?: '+' | '*';
  increment?: number;
  steps?: number;
  mediumStep?: number;
  unit?: 'px' | 'rem' | 'em' | '%' | 'vw' | 'vh';
}

/** Settings sub-tree we expose in the customizer. The backend
 *  accepts the full theme.json shape but the UI only drives these
 *  sections — the rest fall through unchanged. */
export interface ThemeSettings {
  color?: { palette?: ColorEntry[] };
  typography?: { fontFamilies?: FontFamily[]; fontSizes?: FontSize[] };
  layout?: LayoutSettings;
  spacing?: { spacingScale?: SpacingScale };
}

/** Full theme.json manifest (subset that the customizer reads). */
export interface ThemeManifest {
  version: number;
  title?: string;
  settings: ThemeSettings;
}

/** Partial override payload. Same shape as ThemeManifest but every
 *  field is optional — the backend deep-merges onto the base manifest
 *  before validating. */
export interface ThemeOverrides {
  settings?: ThemeSettings;
}

/** GET /active response. */
export interface ActiveResponse {
  themeSlug: string;
  theme: ThemeManifest;
  overrides: ThemeOverrides;
}
