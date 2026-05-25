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
 */
import Link from 'next/link';
import { ArrowRight } from 'lucide-react';
import type { ReactElement } from 'react';

import { Wordmark } from '@/components/brand/Wordmark';

const NAV_LINKS = [
  { href: '/features', label: 'Product' },
  { href: '/templates', label: 'Templates' },
  { href: '/marketplace', label: 'Marketplace' },
  { href: '/pricing', label: 'Pricing' },
  { href: '/customers', label: 'Customers' },
  { href: '/docs', label: 'Docs' },
] as const;

export function MarketingNav(): ReactElement {
  return (
    <nav
      data-surface="forest"
      className="sticky top-4 z-20 mx-auto mt-4 flex max-w-[920px] items-center justify-between rounded-pill bg-ink py-[6px] pl-[18px] pr-[6px] text-paper shadow-md"
      aria-label="Primary"
    >
      <div className="flex items-center gap-7">
        <Link href="/" className="text-paper no-underline">
          <Wordmark surface="forest" size="md" />
        </Link>
        <ul className="hidden gap-[22px] md:flex">
          {NAV_LINKS.map((link) => (
            <li key={link.href}>
              <Link
                href={link.href}
                className="text-sm text-fg-on-forest-muted no-underline transition-colors duration-DEFAULT ease-brand hover:text-paper"
              >
                {link.label}
              </Link>
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
