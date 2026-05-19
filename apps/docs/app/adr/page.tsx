/**
 * /adr landing — Architecture Decision Records index.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { Sidebar } from '@/components/Sidebar';
import { buildNav, listPages } from '@/lib/content';

export default async function AdrIndex(): Promise<ReactElement> {
  const [nodes, pages] = await Promise.all([buildNav('adr'), listPages('adr')]);
  return (
    <div className="docs-shell">
      <Sidebar section="adr" nodes={nodes} />
      <div className="docs-shell__main">
        <div className="docs-shell__content">
          <article className="docs-shell__article">
            <h1>Architecture Decision Records</h1>
            <p>
              Architectural decisions we have made, the context that prompted
              them, and the consequences we accept. Each ADR is dated and
              stamped with a status (proposed, accepted, superseded).
            </p>
            <ul>
              {pages.map((p) => (
                <li key={p.slug}>
                  <Link href={p.slug === '' ? '/adr' : `/adr/${p.slug}`}>{p.title}</Link>
                </li>
              ))}
            </ul>
          </article>
        </div>
      </div>
    </div>
  );
}
