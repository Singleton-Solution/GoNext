/**
 * Marketing footer — forest surface, five link columns + tagline.
 *
 * Mirrors the `footer` block from docs/design/ui_kits/marketing/index.html.
 * The brand-foot wordmark in the first column uses the wordmark
 * primitive with `surface="forest"` so "Next" lifts to emerald-bright,
 * matching the kit's `.brand-foot .wm-next` rule.
 *
 * Server Component — wordmark, tagline, and the © line read from the
 * `core.site.*` registry via `fetchSiteOptions`. Callers can pass the
 * fields in directly when the page already fetched them, but the
 * default-undefined path is the common one (no caller has to know
 * about settings just to render the footer).
 *
 * Link columns: each column reads from a named menu location
 * (`footer-product`, `footer-resources`, `footer-company`,
 * `footer-legal`). An empty/missing menu renders an empty column.
 * The shipping defaults are gone — once an admin can curate the
 * footer, the source of truth has to be the menus store. See #509.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';

import { Wordmark } from '@/components/brand/Wordmark';
import { fetchMenu, fetchSiteOptions, type MenuItem } from '@/lib/api';

interface ColumnProps {
  heading: string;
  links: ReadonlyArray<MenuItem>;
}

function Column({ heading, links }: ColumnProps): ReactElement {
  return (
    <div>
      <h5 className="mb-3 text-xs font-semibold uppercase tracking-[0.06em] text-fg-on-forest">
        {heading}
      </h5>
      <ul className="flex flex-col gap-1">
        {links.map((l) => (
          <li key={`${l.href}:${l.label}`}>
            {l.external ? (
              <a
                href={l.href}
                target="_blank"
                rel="noopener noreferrer"
                className="block py-1 text-sm text-fg-on-forest-muted no-underline transition-colors duration-DEFAULT ease-brand hover:text-fg-on-forest"
              >
                {l.label}
              </a>
            ) : (
              <Link
                href={l.href}
                className="block py-1 text-sm text-fg-on-forest-muted no-underline transition-colors duration-DEFAULT ease-brand hover:text-fg-on-forest"
              >
                {l.label}
              </Link>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

export interface MarketingFooterProps {
  /** Site name from `core.site.name` — used for wordmark + © line. */
  siteName?: string;
  /** Tagline from `core.site.tagline` — used as the brand-column copy. */
  siteTagline?: string;
}

export async function MarketingFooter({
  siteName,
  siteTagline,
}: MarketingFooterProps = {}): Promise<ReactElement> {
  // Settings + four menu columns in parallel — no read depends on
  // another. The menu reads share the same 5-minute revalidate window
  // as the primary nav.
  const needsOpts = siteName === undefined || siteTagline === undefined;
  const [opts, product, resources, company, legal] = await Promise.all([
    needsOpts ? fetchSiteOptions({ revalidate: 60 }) : Promise.resolve(null),
    fetchMenu('footer-product', { revalidate: 300 }),
    fetchMenu('footer-resources', { revalidate: 300 }),
    fetchMenu('footer-company', { revalidate: 300 }),
    fetchMenu('footer-legal', { revalidate: 300 }),
  ]);
  const resolvedName = siteName ?? opts?.name ?? '';
  const resolvedTagline = siteTagline ?? opts?.tagline ?? '';

  return (
    <footer
      data-surface="forest"
      className="bg-forest pb-8 pt-[56px] text-fg-on-forest"
    >
      <div className="mx-auto max-w-[1240px] px-8">
        <div className="mb-10 grid gap-10 md:grid-cols-[2fr_repeat(4,1fr)]">
          <div>
            <Link
              href="/"
              className="inline-flex items-baseline gap-px no-underline"
              aria-label={resolvedName}
            >
              <Wordmark surface="forest" size="md" name={resolvedName} />
            </Link>
            <p className="mt-3.5 max-w-[280px] text-sm leading-[1.5] text-fg-on-forest-muted">
              {resolvedTagline}
            </p>
          </div>
          <Column heading="Product" links={product} />
          <Column heading="Resources" links={resources} />
          <Column heading="Company" links={company} />
          <Column heading="Legal" links={legal} />
        </div>
        <div className="flex items-center justify-between border-t border-forest-border pt-6 text-xs text-fg-on-forest-muted">
          <span>© {new Date().getFullYear()} {resolvedName}</span>
          <span className="font-mono">
            v1.0 ·{' '}
            <span className="text-fg-on-forest">
              built on Go & Next.js
            </span>
          </span>
        </div>
      </div>
    </footer>
  );
}
