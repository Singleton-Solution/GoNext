/**
 * /docs landing — section index.
 *
 * Lists every doc with its title. Rendered fully at build time from
 * `listPages('docs')` so it stays in sync with the on-disk markdown
 * without ever consulting a database.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { Sidebar } from '@/components/Sidebar';
import { buildNav, listPages } from '@/lib/content';

export default async function DocsIndex(): Promise<ReactElement> {
  const [nodes, pages] = await Promise.all([buildNav('docs'), listPages('docs')]);
  return (
    <div className="docs-shell">
      <Sidebar section="docs" nodes={nodes} />
      <div className="docs-shell__main">
        <div className="docs-shell__content">
          <article className="docs-shell__article">
            <h1>Documentation</h1>
            <p>
              Subsystem-level guides for building, operating, and extending GoNext.
              Each doc is self-contained — the architecture overview is the entry point.
            </p>
            <ul>
              {pages.map((p) => (
                <li key={p.slug}>
                  <Link href={p.slug === '' ? '/docs' : `/docs/${p.slug}`}>{p.title}</Link>
                  {p.description && <span style={{ color: 'var(--color-text-muted)' }}> — {p.description}</span>}
                </li>
              ))}
            </ul>
          </article>
        </div>
      </div>
    </div>
  );
}
