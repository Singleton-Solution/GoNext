/**
 * Right-rail "On this page" table of contents.
 *
 * Built from the `headings` array produced by `lib/mdx.tsx` during the
 * server render — we never run a DOM walk on the client. Anchors are the
 * same ids that `rehype-slug` / `slugify` emit, so clicks resolve natively.
 *
 * Visual depth is capped at h2/h3 because h1 is the page title (already
 * shown in the header) and h4+ produces noise in long-form docs.
 */
import type { ReactElement } from 'react';
import type { Heading } from '@/lib/mdx';

export function TocSidebar({ headings }: { headings: Heading[] }): ReactElement | null {
  const visible = headings.filter((h) => h.depth === 2 || h.depth === 3);
  if (visible.length === 0) return null;
  return (
    <aside className="toc" aria-label="On this page">
      <div className="toc__heading">On this page</div>
      <ul className="toc__list">
        {visible.map((h, idx) => (
          <li key={`${h.id}-${idx}`} className={`toc__item toc__item--depth-${h.depth}`}>
            <a href={`#${h.id}`} className="toc__link">
              {h.text}
            </a>
          </li>
        ))}
      </ul>
    </aside>
  );
}
