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

/** Spacing token slugs surfaced by the advanced scale editor. The
 *  customizer drives these six tokens directly — the backend accepts
 *  them as `settings.spacing.spacingSizes[]` entries (one per slug). */
export type SpacingTokenSlug = 'xs' | 'sm' | 'md' | 'lg' | 'xl' | '2xl';

/** One token in the spacing scale (xs … 2xl). The value is a CSS
 *  length string — typically `rem` for the customizer, but the
 *  validator accepts any of the SpacingScale units. */
export interface SpacingSize {
  slug: SpacingTokenSlug;
  name: string;
  size: string;
}

/** Shadow preset slugs surfaced by the advanced shadow editor. */
export type ShadowPresetSlug = 'sm' | 'md' | 'lg' | 'xl';

/** One preset in the shadow editor. Split into the underlying CSS
 *  pieces (offset / blur / spread / color) so each control can be a
 *  slider. The serialised form is recomposed at save time. */
export interface ShadowPreset {
  slug: ShadowPresetSlug;
  name: string;
  offsetX: number;
  offsetY: number;
  blur: number;
  spread: number;
  color: string;
}

/** Breakpoint slugs surfaced by the responsive editor. */
export type BreakpointSlug = 'sm' | 'md' | 'lg' | 'xl';

/** One breakpoint entry. The width is the min-width in px the
 *  renderer uses to gate the matching media query. */
export interface Breakpoint {
  slug: BreakpointSlug;
  name: string;
  width: number;
}

/** Custom (non-theme.json-standard) settings the advanced surface
 *  introduces. The backend deep-merges them under
 *  `settings.custom.gonext.*` so they round-trip through the same
 *  validator without needing a schema bump. */
export interface CustomSettings {
  spacingTokens?: SpacingSize[];
  shadowPresets?: ShadowPreset[];
  breakpoints?: Breakpoint[];
}

/** Settings sub-tree we expose in the customizer. The backend
 *  accepts the full theme.json shape but the UI only drives these
 *  sections — the rest fall through unchanged. */
export interface ThemeSettings {
  color?: { palette?: ColorEntry[] };
  typography?: { fontFamilies?: FontFamily[]; fontSizes?: FontSize[] };
  layout?: LayoutSettings;
  spacing?: { spacingScale?: SpacingScale };
  custom?: { gonext?: CustomSettings };
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
