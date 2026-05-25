/**
 * Themes & Appearance — theme browser landing.
 *
 * The top of the Appearance section: a gallery of installed themes the
 * operator can preview and activate. Each card shows a stylised
 * screenshot (rendered as an in-line preview frame so the gallery
 * doesn't depend on per-theme bitmap assets), the theme's display
 * name in Archivo, a one-line description that uses the italic-accent
 * rule, and an emerald "Activate" CTA.
 *
 * The active theme card is called out with an emerald-bright border
 * and a corner badge — the brand's signature "alive" emphasis.
 *
 * Visual spec: docs/design/ui_kits/templates/index.html — same
 * "library gallery" treatment the marketing screen uses, just tuned
 * to admin chrome (no top hero nav, tighter spacing).
 *
 * The themes list is hard-coded for now: the API endpoint that lists
 * installed themes lands in a follow-up issue. The data shape mirrors
 * what that endpoint will return so swapping to a real fetch is a
 * one-line edit. Active-theme selection still round-trips through the
 * existing /api/v1/admin/customizer/active call (the active slug is
 * authoritative on the server side).
 */
import type { ReactElement } from 'react';
import { fetchActive } from './customizer/api';
import { ThemeBrowser } from './ThemeBrowser';
import type { ThemeCard } from './ThemeBrowser';

export const dynamic = 'force-dynamic';

/**
 * Curated themes shown in the browser. The previews are CSS-only so
 * the gallery renders identically across environments without per-
 * theme image assets.
 */
const THEMES: readonly ThemeCard[] = [
  {
    slug: 'gn-hello',
    name: 'Hello',
    description: 'A *living* starter theme. Editorial blog scaffolding with paywall lane.',
    tags: ['Editorial', 'Blog'],
    preview: 'editorial',
  },
  {
    slug: 'broadsheet',
    name: 'Broadsheet',
    description: 'Long-form publication theme. Photo essays that *grow* with your archive.',
    tags: ['Editorial', 'Paywall'],
    preview: 'editorial',
  },
  {
    slug: 'counter',
    name: 'Counter',
    description: 'Single-product commerce. Subscriptions, gift cards, *built-in* configurator.',
    tags: ['Commerce', 'Stripe'],
    preview: 'shop',
  },
  {
    slug: 'studio-mono',
    name: 'Studio Mono',
    description: 'High-contrast agency template. Case studies that *speak*, calendar booking.',
    tags: ['Agency', 'Portfolio'],
    preview: 'studio',
  },
  {
    slug: 'portfolio-leaf',
    name: 'Portfolio',
    description: 'Personal site for *makers*. Project index, journal, contact.',
    tags: ['Portfolio'],
    preview: 'portfolio',
  },
  {
    slug: 'docs-clean',
    name: 'Docs',
    description: 'A docs site that *reads* well. Sidenav, search, version pill.',
    tags: ['Docs', 'Reference'],
    preview: 'docs',
  },
];

export default async function AppearancePage(): Promise<ReactElement> {
  // Resolve the active theme slug from the customizer endpoint so the
  // "active" badge tracks the same source of truth the customizer
  // edits. Falls back to the conventional default when the API is
  // unavailable — we still want the gallery to render.
  const active = await fetchActive();
  const activeSlug = active.available ? active.data.themeSlug : 'gn-hello';

  return <ThemeBrowser themes={THEMES} activeSlug={activeSlug} />;
}
