/**
 * Theme Customizer — state helpers.
 *
 * Pure functions used by the page component and by tests. Kept separate
 * from `page.tsx` so the reducer + URL builder can be exercised without
 * mounting the full React tree.
 */
import type {
  ActiveResponse,
  Breakpoint,
  ColorEntry,
  FontFamily,
  FontSize,
  LayoutSettings,
  ShadowPreset,
  SpacingScale,
  SpacingSize,
  ThemeOverrides,
  ThemeSettings,
} from './types';

/** Defaults used by the advanced surface when the active theme doesn't
 *  declare custom tokens. These values mirror Tailwind's defaults for
 *  spacing/shadows/breakpoints — every WP-using designer will
 *  recognise the steps and reach for them by muscle memory. */
const DEFAULT_SPACING_TOKENS: SpacingSize[] = [
  { slug: 'xs', name: 'Extra small', size: '0.25rem' },
  { slug: 'sm', name: 'Small', size: '0.5rem' },
  { slug: 'md', name: 'Medium', size: '1rem' },
  { slug: 'lg', name: 'Large', size: '1.5rem' },
  { slug: 'xl', name: 'Extra large', size: '2rem' },
  { slug: '2xl', name: '2× extra large', size: '3rem' },
];
const DEFAULT_SHADOW_PRESETS: ShadowPreset[] = [
  { slug: 'sm', name: 'Small', offsetX: 0, offsetY: 1, blur: 2, spread: 0, color: 'rgba(0,0,0,0.08)' },
  { slug: 'md', name: 'Medium', offsetX: 0, offsetY: 4, blur: 8, spread: 0, color: 'rgba(0,0,0,0.12)' },
  { slug: 'lg', name: 'Large', offsetX: 0, offsetY: 8, blur: 16, spread: 0, color: 'rgba(0,0,0,0.16)' },
  { slug: 'xl', name: 'Extra large', offsetX: 0, offsetY: 16, blur: 32, spread: 0, color: 'rgba(0,0,0,0.2)' },
];
const DEFAULT_BREAKPOINTS: Breakpoint[] = [
  { slug: 'sm', name: 'Small', width: 640 },
  { slug: 'md', name: 'Medium', width: 768 },
  { slug: 'lg', name: 'Large', width: 1024 },
  { slug: 'xl', name: 'Extra large', width: 1280 },
];

/** Read the advanced custom tokens with a fallback chain:
 *  overrides → theme defaults → hard-coded defaults. The hard-coded
 *  layer ensures the advanced editors always render even on a brand
 *  new theme that hasn't declared them yet. */
function readCustom<T>(
  overrideValue: T[] | undefined,
  themeValue: T[] | undefined,
  fallback: T[],
): T[] {
  if (overrideValue && overrideValue.length > 0) return clone(overrideValue);
  if (themeValue && themeValue.length > 0) return clone(themeValue);
  return clone(fallback);
}

/** Customizer state shape. Mirrors the editable shape of a theme, with
 *  every field directly bindable to a form control. The reducer
 *  guarantees the palette/fontFamilies/fontSizes arrays are never
 *  shared with the upstream manifest — mutations always produce a
 *  fresh array. */
export interface CustomizerState {
  palette: ColorEntry[];
  fontFamilies: FontFamily[];
  fontSizes: FontSize[];
  layout: LayoutSettings;
  spacing: SpacingScale;
  spacingTokens: SpacingSize[];
  shadowPresets: ShadowPreset[];
  breakpoints: Breakpoint[];
}

/** Build the initial state from the active-theme response. Overrides
 *  win field-by-field; missing overrides fall through to the theme
 *  defaults. The result is a deep copy so the UI can mutate without
 *  touching the upstream response (which the page also keeps for
 *  Reset). */
export function initialState(active: ActiveResponse): CustomizerState {
  const o = active.overrides?.settings ?? {};
  const t = active.theme?.settings ?? {};
  const oCustom = o.custom?.gonext ?? {};
  const tCustom = t.custom?.gonext ?? {};
  return {
    palette: clone(o.color?.palette ?? t.color?.palette ?? []),
    fontFamilies: clone(
      o.typography?.fontFamilies ?? t.typography?.fontFamilies ?? [],
    ),
    fontSizes: clone(
      o.typography?.fontSizes ?? t.typography?.fontSizes ?? [],
    ),
    layout: { ...(o.layout ?? t.layout ?? {}) },
    spacing: { ...(o.spacing?.spacingScale ?? t.spacing?.spacingScale ?? {}) },
    spacingTokens: readCustom(
      oCustom.spacingTokens,
      tCustom.spacingTokens,
      DEFAULT_SPACING_TOKENS,
    ),
    shadowPresets: readCustom(
      oCustom.shadowPresets,
      tCustom.shadowPresets,
      DEFAULT_SHADOW_PRESETS,
    ),
    breakpoints: readCustom(
      oCustom.breakpoints,
      tCustom.breakpoints,
      DEFAULT_BREAKPOINTS,
    ),
  };
}

/** Defaults exported for tests + reset semantics. Consumers must not
 *  mutate the returned array — call through `clone` if you need a
 *  writable copy. */
export const ADVANCED_DEFAULTS = {
  spacingTokens: DEFAULT_SPACING_TOKENS,
  shadowPresets: DEFAULT_SHADOW_PRESETS,
  breakpoints: DEFAULT_BREAKPOINTS,
} as const;

/** Build the override payload from the current state by diffing
 *  against the manifest defaults. The diff is field-aware: a section
 *  is included if any leaf differs from the theme default. */
export function buildOverrides(
  state: CustomizerState,
  theme: ActiveResponse['theme'],
): ThemeOverrides {
  const t = theme.settings ?? {};
  const overrides: ThemeSettings = {};

  if (!arraysEqual(state.palette, t.color?.palette ?? [], paletteEq)) {
    overrides.color = { palette: clone(state.palette) };
  }
  const typography: ThemeSettings['typography'] = {};
  if (
    !arraysEqual(state.fontFamilies, t.typography?.fontFamilies ?? [], fontFamilyEq)
  ) {
    typography.fontFamilies = clone(state.fontFamilies);
  }
  if (!arraysEqual(state.fontSizes, t.typography?.fontSizes ?? [], fontSizeEq)) {
    typography.fontSizes = clone(state.fontSizes);
  }
  if (typography.fontFamilies || typography.fontSizes) {
    overrides.typography = typography;
  }
  const baseLayout = t.layout ?? {};
  if (
    state.layout.contentSize !== baseLayout.contentSize ||
    state.layout.wideSize !== baseLayout.wideSize
  ) {
    overrides.layout = { ...state.layout };
  }
  const baseSpacing = t.spacing?.spacingScale ?? {};
  if (
    !shallowEqual(
      state.spacing as Record<string, unknown>,
      baseSpacing as Record<string, unknown>,
    )
  ) {
    if (Object.keys(state.spacing).length > 0) {
      overrides.spacing = { spacingScale: { ...state.spacing } };
    }
  }

  // Advanced surface — spacing tokens, shadow presets, breakpoints.
  // Each lives under `settings.custom.gonext.*` so the validator
  // round-trips it without needing a top-level schema field.
  const baseCustom = t.custom?.gonext ?? {};
  const customDiff: Record<string, unknown> = {};
  if (
    !arraysEqual(
      state.spacingTokens,
      baseCustom.spacingTokens ?? DEFAULT_SPACING_TOKENS,
      spacingSizeEq,
    )
  ) {
    customDiff.spacingTokens = clone(state.spacingTokens);
  }
  if (
    !arraysEqual(
      state.shadowPresets,
      baseCustom.shadowPresets ?? DEFAULT_SHADOW_PRESETS,
      shadowPresetEq,
    )
  ) {
    customDiff.shadowPresets = clone(state.shadowPresets);
  }
  if (
    !arraysEqual(
      state.breakpoints,
      baseCustom.breakpoints ?? DEFAULT_BREAKPOINTS,
      breakpointEq,
    )
  ) {
    customDiff.breakpoints = clone(state.breakpoints);
  }
  if (Object.keys(customDiff).length > 0) {
    overrides.custom = { gonext: customDiff };
  }
  return { settings: overrides };
}

/** Returns true when the override payload has no editable fields. The
 *  page uses this to gate the Save button — saving an empty override
 *  is the same as reset, and the backend explicitly rejects an empty
 *  body to make the distinction loud. */
export function isOverrideEmpty(overrides: ThemeOverrides): boolean {
  const s = overrides.settings ?? {};
  return (
    !s.color &&
    !s.typography &&
    !s.layout &&
    !s.spacing &&
    !s.custom
  );
}

/** Encode the overrides as base64-url-safe JSON for the preview iframe
 *  URL. The renderer's preview mode decodes this and merges it onto
 *  the active theme without persisting — so operators see their
 *  unsaved tweaks before committing. */
export function encodePreviewOverrides(overrides: ThemeOverrides): string {
  const json = JSON.stringify(overrides);
  // btoa is unavailable in Node (Vitest jsdom env supplies it, so this
  // works in tests + browser). The polyfill below covers the SSR path
  // where the function might be invoked during a server render — the
  // page itself is client-only, but encodePreviewOverrides is exported
  // so a future server component can construct preview URLs too.
  if (typeof btoa === 'function') {
    return btoa(json).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  // Node fallback — never hit in production but keeps the unit test
  // green when run under a bare Node runner.
  return Buffer.from(json, 'utf8')
    .toString('base64')
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

/** Build the preview iframe URL. The public-site renderer treats the
 *  `customizer=preview` flag as the trigger for applying inline
 *  overrides — same shape WordPress's customize_preview_link uses. */
export function previewUrl(baseUrl: string, overrides: ThemeOverrides): string {
  const encoded = encodePreviewOverrides(overrides);
  const sep = baseUrl.includes('?') ? '&' : '?';
  return `${baseUrl}${sep}customizer=preview&overrides=${encoded}`;
}

// Small helpers — exported only via tests.

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function paletteEq(a: ColorEntry, b: ColorEntry): boolean {
  return a.slug === b.slug && a.name === b.name && a.color === b.color;
}
function fontFamilyEq(a: FontFamily, b: FontFamily): boolean {
  return a.slug === b.slug && a.name === b.name && a.fontFamily === b.fontFamily;
}
function fontSizeEq(a: FontSize, b: FontSize): boolean {
  return a.slug === b.slug && a.name === b.name && a.size === b.size;
}
function spacingSizeEq(a: SpacingSize, b: SpacingSize): boolean {
  return a.slug === b.slug && a.name === b.name && a.size === b.size;
}
function shadowPresetEq(a: ShadowPreset, b: ShadowPreset): boolean {
  return (
    a.slug === b.slug &&
    a.name === b.name &&
    a.offsetX === b.offsetX &&
    a.offsetY === b.offsetY &&
    a.blur === b.blur &&
    a.spread === b.spread &&
    a.color === b.color
  );
}
function breakpointEq(a: Breakpoint, b: Breakpoint): boolean {
  return a.slug === b.slug && a.name === b.name && a.width === b.width;
}
function arraysEqual<T>(a: T[], b: T[], eq: (x: T, y: T) => boolean): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    // The length check above guarantees both indices are defined.
    if (!eq(a[i] as T, b[i] as T)) return false;
  }
  return true;
}
function shallowEqual(a: Record<string, unknown>, b: Record<string, unknown>): boolean {
  const ak = Object.keys(a);
  const bk = Object.keys(b);
  if (ak.length !== bk.length) return false;
  for (const k of ak) {
    if (a[k] !== b[k]) return false;
  }
  return true;
}

export const __testing = {
  clone,
  paletteEq,
  fontFamilyEq,
  fontSizeEq,
  spacingSizeEq,
  shadowPresetEq,
  breakpointEq,
};
