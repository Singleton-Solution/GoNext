/**
 * Built-in pattern categories.
 *
 * Patterns live in a flat list under the `PatternRegistry`, but the
 * editor groups them in the inserter by category. We expose a constant
 * list here so the inserter UI can render category tabs in a stable
 * order without having to derive the set from the registered patterns
 * (which can vary across installs and plugin loads).
 *
 * Plugin authors may register patterns under arbitrary category strings;
 * the editor falls back to grouping unknown values under "Other".
 */
export type PatternCategory =
  | 'hero'
  | 'features'
  | 'pricing'
  | 'testimonials'
  | 'cta'
  | 'gallery'
  | 'contact'
  | 'header'
  | 'footer'
  | 'posts'
  | (string & {});

/**
 * The set of pattern categories the editor renders as built-in tabs, in
 * display order. Mirrors the structure WordPress uses for its first-party
 * patterns ("Featured", "Buttons", "Columns", …) but with the categories
 * curated for the GoNext starter set.
 */
export const BUILT_IN_PATTERN_CATEGORIES: readonly PatternCategory[] = [
  'hero',
  'features',
  'pricing',
  'testimonials',
  'cta',
  'gallery',
  'contact',
  'header',
  'footer',
  'posts',
] as const;

/**
 * Human-readable label for a built-in category. The inserter uses this
 * to render tab titles; unknown categories fall through to the raw key
 * so plugin-defined categories still surface visibly.
 */
export const PATTERN_CATEGORY_LABELS: Readonly<
  Record<PatternCategory, string>
> = {
  hero: 'Hero',
  features: 'Features',
  pricing: 'Pricing',
  testimonials: 'Testimonials',
  cta: 'Call to Action',
  gallery: 'Gallery',
  contact: 'Contact',
  header: 'Header',
  footer: 'Footer',
  posts: 'Posts',
};
