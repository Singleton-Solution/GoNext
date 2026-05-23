/**
 * @gonext/blocks-patterns — public entry point.
 *
 * Ships the **first-party** patterns every GoNext install relies on as
 * starter shapes. The original ten patterns from #401 are joined here
 * by ten more, lifting the catalogue to twenty:
 *
 *  Original ten:
 *  - `core/hero-with-cta`         — wide heading + body + two CTAs
 *  - `core/three-column-features` — three feature cards in a column track
 *  - `core/pricing-three-tier`    — starter / pro / enterprise pricing grid
 *  - `core/testimonial-grid`      — two pull-quoted testimonials side by side
 *  - `core/cta-banner`            — focused conversion banner with one CTA
 *  - `core/gallery-masonry`       — 3-column gallery preserving aspect ratios
 *  - `core/contact-form`          — contact section with email CTA + list
 *  - `core/header-logo-nav`       — `<header>` with brand + nav columns
 *  - `core/footer-multi-column`   — four-column `<footer>` + colophon
 *  - `core/post-grid`             — three-card post grid for the home page
 *
 *  New in this revision:
 *  - `core/faq-accordion`             — heading + Q/A pairs
 *  - `core/team-grid`                 — three-column meet-the-team grid
 *  - `core/stat-counter-row`          — four-column "big number" row
 *  - `core/newsletter-signup`         — compact newsletter CTA
 *  - `core/comparison-table`          — feature-by-feature comparison table
 *  - `core/bullet-list-with-icons`    — heading + benefits list
 *  - `core/timeline-vertical`         — heading + stacked milestone entries
 *  - `core/quote-with-portrait`       — featured testimonial with portrait
 *  - `core/image-text-split`          — image left, copy right
 *  - `core/image-text-split-reversed` — copy left, image right
 *
 * Designed for tree-shaking: every symbol is a named export. The package
 * carries no side-effects at module-evaluation time — call
 * `registerCorePatterns(registry)` explicitly to populate the registry.
 *
 * Most consumers will only ever call `registerCorePatterns(registry)`.
 * The individual pattern exports are available so apps can swap one out
 * (e.g. theme-specific hero) while keeping the rest.
 */

import { PatternRegistry } from './registry.ts';

import { heroWithCta } from './patterns/hero-with-cta.ts';
import { threeColumnFeatures } from './patterns/three-column-features.ts';
import { pricingThreeTier } from './patterns/pricing-three-tier.ts';
import { testimonialGrid } from './patterns/testimonial-grid.ts';
import { ctaBanner } from './patterns/cta-banner.ts';
import { galleryMasonry } from './patterns/gallery-masonry.ts';
import { contactForm } from './patterns/contact-form.ts';
import { headerLogoNav } from './patterns/header-logo-nav.ts';
import { footerMultiColumn } from './patterns/footer-multi-column.ts';
import { postGrid } from './patterns/post-grid.ts';
import { faqAccordion } from './patterns/faq-accordion.ts';
import { teamGrid } from './patterns/team-grid.ts';
import { statCounterRow } from './patterns/stat-counter-row.ts';
import { newsletterSignup } from './patterns/newsletter-signup.ts';
import { comparisonTable } from './patterns/comparison-table.ts';
import { bulletListWithIcons } from './patterns/bullet-list-with-icons.ts';
import { timelineVertical } from './patterns/timeline-vertical.ts';
import { quoteWithPortrait } from './patterns/quote-with-portrait.ts';
import { imageTextSplit } from './patterns/image-text-split.ts';
import { imageTextSplitReversed } from './patterns/image-text-split-reversed.ts';

// Per-pattern re-exports so consumers can grab a single pattern by name.
export { heroWithCta } from './patterns/hero-with-cta.ts';
export { threeColumnFeatures } from './patterns/three-column-features.ts';
export { pricingThreeTier } from './patterns/pricing-three-tier.ts';
export { testimonialGrid } from './patterns/testimonial-grid.ts';
export { ctaBanner } from './patterns/cta-banner.ts';
export { galleryMasonry } from './patterns/gallery-masonry.ts';
export { contactForm } from './patterns/contact-form.ts';
export { headerLogoNav } from './patterns/header-logo-nav.ts';
export { footerMultiColumn } from './patterns/footer-multi-column.ts';
export { postGrid } from './patterns/post-grid.ts';
export { faqAccordion } from './patterns/faq-accordion.ts';
export { teamGrid } from './patterns/team-grid.ts';
export { statCounterRow } from './patterns/stat-counter-row.ts';
export { newsletterSignup } from './patterns/newsletter-signup.ts';
export { comparisonTable } from './patterns/comparison-table.ts';
export { bulletListWithIcons } from './patterns/bullet-list-with-icons.ts';
export { timelineVertical } from './patterns/timeline-vertical.ts';
export { quoteWithPortrait } from './patterns/quote-with-portrait.ts';
export { imageTextSplit } from './patterns/image-text-split.ts';
export { imageTextSplitReversed } from './patterns/image-text-split-reversed.ts';

// Type + category surface re-exports.
export type { Pattern } from './types.ts';
export {
  BUILT_IN_PATTERN_CATEGORIES,
  PATTERN_CATEGORY_LABELS,
  type PatternCategory,
} from './categories.ts';

// Registry surface re-exports.
export {
  DuplicatePatternError,
  PatternRegistry,
  type RegisterPatternOptions,
} from './registry.ts';

/**
 * The complete ordered list of every first-party pattern, in the order
 * they appear in the inserter. Consumer code can iterate this list to
 * snapshot test the catalog, or to drive a per-pattern UI inventory
 * outside of the registry path.
 *
 * Ordering rule: the original ten patterns from #401 stay in their
 * existing positions so any downstream snapshot or index-keyed test
 * keeps passing; the ten additions are appended at the end.
 */
export const CORE_PATTERNS = [
  heroWithCta,
  threeColumnFeatures,
  pricingThreeTier,
  testimonialGrid,
  ctaBanner,
  galleryMasonry,
  contactForm,
  headerLogoNav,
  footerMultiColumn,
  postGrid,
  faqAccordion,
  teamGrid,
  statCounterRow,
  newsletterSignup,
  comparisonTable,
  bulletListWithIcons,
  timelineVertical,
  quoteWithPortrait,
  imageTextSplit,
  imageTextSplitReversed,
] as const;

/**
 * Register every first-party pattern on a given `PatternRegistry`.
 * Mirrors the `registerCoreBlocks(...)` shape exposed by
 * `@gonext/blocks-core` so apps that already wired the latter can pick
 * up patterns with a parallel one-line call.
 *
 * Pass `{ replace: true }` only for HMR-style reloads — production code
 * should leave it off so a duplicate registration throws loudly via
 * `DuplicatePatternError`.
 */
export function registerCorePatterns(
  registry: PatternRegistry,
  options: { replace?: boolean } = {},
): void {
  for (const pattern of CORE_PATTERNS) {
    registry.register(pattern, options);
  }
}
