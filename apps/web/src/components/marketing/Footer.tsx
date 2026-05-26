/**
 * Marketing footer — forest surface, five link columns + tagline.
 *
 * Mirrors the `footer` block from docs/design/ui_kits/marketing/index.html.
 * The brand-foot wordmark in the first column uses the wordmark
 * primitive with `surface="forest"` so "Next" lifts to emerald-bright,
 * matching the kit's `.brand-foot .wm-next` rule.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';

import { Wordmark } from '@/components/brand/Wordmark';

const PRODUCT = [
  { href: '/editor', label: 'Editor' },
  { href: '/hosting', label: 'Hosting' },
  { href: '/commerce', label: 'Commerce' },
  { href: '/analytics', label: 'Analytics' },
  { href: '/marketplace', label: 'Marketplace' },
];

const RESOURCES = [
  { href: '/docs', label: 'Docs' },
  { href: '/changelog', label: 'Changelog' },
  { href: '/status', label: 'Status' },
  { href: '/importer', label: 'Importer' },
  { href: '/api', label: 'API' },
];

const COMPANY = [
  { href: '/about', label: 'About' },
  { href: '/customers', label: 'Customers' },
  { href: '/careers', label: 'Careers' },
  { href: '/press', label: 'Press' },
];

const LEGAL = [
  { href: '/privacy', label: 'Privacy' },
  { href: '/terms', label: 'Terms' },
  { href: '/security', label: 'Security' },
  { href: '/dpa', label: 'DPA' },
];

interface ColumnProps {
  heading: string;
  links: ReadonlyArray<{ href: string; label: string }>;
}

function Column({ heading, links }: ColumnProps): ReactElement {
  return (
    <div>
      <h5 className="mb-3 text-xs font-semibold uppercase tracking-[0.06em] text-fg-on-forest">
        {heading}
      </h5>
      <ul className="flex flex-col gap-1">
        {links.map((l) => (
          <li key={l.href}>
            <Link
              href={l.href}
              className="block py-1 text-sm text-fg-on-forest-muted no-underline transition-colors duration-DEFAULT ease-brand hover:text-fg-on-forest"
            >
              {l.label}
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function MarketingFooter(): ReactElement {
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
              aria-label="GoNext"
            >
              <Wordmark surface="forest" size="md" />
            </Link>
            <p className="mt-3.5 max-w-[280px] text-sm leading-[1.5] text-fg-on-forest-muted">
              An all-in-one platform for content, hosting, and commerce.
              Built on Go and Next.js — a system designed to grow with the
              sites running on it.
            </p>
          </div>
          <Column heading="Product" links={PRODUCT} />
          <Column heading="Resources" links={RESOURCES} />
          <Column heading="Company" links={COMPANY} />
          <Column heading="Legal" links={LEGAL} />
        </div>
        <div className="flex items-center justify-between border-t border-forest-border pt-6 text-xs text-fg-on-forest-muted">
          <span>© {new Date().getFullYear()} GoNext, Inc.</span>
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
