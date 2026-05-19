/**
 * Prev/next footer navigation for a doc page.
 *
 * Takes the flat ordered page list from `listPages()` and finds the
 * neighbours of the current slug. Pure render, no client state.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import type { PageMeta, Section } from '@/lib/content';

interface PageNavProps {
  pages: PageMeta[];
  currentSlug: string;
  section: Section;
}

export function PageNav({ pages, currentSlug, section }: PageNavProps): ReactElement | null {
  const idx = pages.findIndex((p) => p.slug === currentSlug);
  if (idx === -1) return null;
  const prev = idx > 0 ? pages[idx - 1] : undefined;
  const next = idx < pages.length - 1 ? pages[idx + 1] : undefined;
  if (!prev && !next) return null;

  const hrefFor = (p: PageMeta) => (p.slug === '' ? `/${section}` : `/${section}/${p.slug}`);

  return (
    <nav className="page-nav" aria-label="Pagination">
      <div className="page-nav__cell page-nav__cell--prev">
        {prev && (
          <Link href={hrefFor(prev)} className="page-nav__link">
            <span className="page-nav__label">Previous</span>
            <span className="page-nav__title">{prev.title}</span>
          </Link>
        )}
      </div>
      <div className="page-nav__cell page-nav__cell--next">
        {next && (
          <Link href={hrefFor(next)} className="page-nav__link page-nav__link--next">
            <span className="page-nav__label">Next</span>
            <span className="page-nav__title">{next.title}</span>
          </Link>
        )}
      </div>
    </nav>
  );
}
