/**
 * Marketing top navigation — the sticky forest pill.
 *
 * Mirrors `nav.top` from docs/design/ui_kits/marketing/index.html.
 * Sits 16px from the top of the viewport, pill-shaped forest surface
 * with the wordmark on the left, six secondary links in the middle,
 * and a cream CTA on the right.
 *
 * The CTA copy is the brand's "Start a site" verb — the on-brand
 * phrasing from HANDOFF.md: confident, quiet, alive.
 *
 * Server Component — accepts an optional `siteName` prop so callers
 * that already pulled the settings registry (e.g. a route page) can
 * pass it down without a second fetch. When omitted we read the
 * registry inline, falling back to the brand default on any failure.
 *
 * Link source: `fetchMenu('primary')` reads the operator-curated
 * primary menu. When no menu is configured we fall back to
 * `DEFAULT_PRIMARY` so a fresh GoNext install still ships a usable
 * nav. See #509.
 */
import Link from 'next/link';
import { ArrowRight } from 'lucide-react';
import type { ReactElement } from 'react';

import { Wordmark } from '@/components/brand/Wordmark';
import { fetchMenu, fetchSiteOptions, type MenuItem } from '@/lib/api';

/**
 * Fallback nav painted when no `primary` menu has been configured in
 * the admin. Matches the original shipping default so a brand-new
 * install renders a finished-looking nav out of the box.
 */
const DEFAULT_PRIMARY: ReadonlyArray<MenuItem> = [
  { href: '/features', label: 'Product', external: false },
  { href: '/templates', label: 'Templates', external: false },
  { href: '/marketplace', label: 'Marketplace', external: false },
  { href: '/pricing', label: 'Pricing', external: false },
  { href: '/customers', label: 'Customers', external: false },
  { href: '/docs', label: 'Docs', external: false },
];

export interface MarketingNavProps {
  /**
   * Site name from `core.site.name`. When omitted the component reads
   * settings inline. Passing it from the caller avoids a duplicate
   * settings fetch when the same page already needed the options.
   */
  siteName?: string;
}

export async function MarketingNav({
  siteName,
}: MarketingNavProps = {}): Promise<ReactElement> {
  // Fetch site name + primary menu in parallel — independent reads.
  // Revalidate every 5 min for the menu (operator-driven, rare edits);
  // the settings registry has its own 60s window upstream.
  const [resolvedName, items] = await Promise.all([
    siteName !== undefined
      ? Promise.resolve(siteName)
      : fetchSiteOptions({ revalidate: 60 }).then((o) => o.name),
    fetchMenu('primary', { revalidate: 300 }),
  ]);
  const links: ReadonlyArray<MenuItem> =
    items.length > 0 ? items : DEFAULT_PRIMARY;

  return (
    <nav
      data-surface="forest"
      className="sticky top-4 z-20 mx-auto mt-4 flex max-w-[920px] items-center justify-between rounded-pill bg-ink py-[6px] pl-[18px] pr-[6px] text-paper shadow-md"
      aria-label="Primary"
    >
      <div className="flex items-center gap-7">
        <Link href="/" className="text-paper no-underline">
          <Wordmark surface="forest" size="md" name={resolvedName} />
        </Link>
        <ul className="hidden gap-[22px] md:flex">
          {links.map((link) => (
            <li key={`${link.href}:${link.label}`}>
              {link.external ? (
                <a
                  href={link.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-sm text-fg-on-forest-muted no-underline transition-colors duration-DEFAULT ease-brand hover:text-paper"
                >
                  {link.label}
                </a>
              ) : (
                <Link
                  href={link.href}
                  className="text-sm text-fg-on-forest-muted no-underline transition-colors duration-DEFAULT ease-brand hover:text-paper"
                >
                  {link.label}
                </Link>
              )}
            </li>
          ))}
        </ul>
      </div>
      <Link
        href="/start"
        className="inline-flex items-center gap-1.5 rounded-pill bg-paper px-4 py-[7px] text-sm font-medium text-ink no-underline transition-colors duration-DEFAULT ease-brand hover:bg-paper-2"
      >
        Start a site
        <ArrowRight className="size-[13px]" aria-hidden />
      </Link>
    </nav>
  );
}
