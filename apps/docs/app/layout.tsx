/**
 * Root layout for @gonext/docs.
 *
 * Renders the persistent shell: top nav (Docs / ADRs / API Reference /
 * Proposals), the section sidebar (slot via `children`), and the search
 * bar. The sidebar is itself rendered by individual section layouts so
 * that pages outside the doc tree (landing, API reference) can render
 * full-bleed.
 */
import type { Metadata } from 'next';
import type { ReactElement, ReactNode } from 'react';
import Link from 'next/link';
import { SearchBar } from '@/components/SearchBar';
import { buildSearchIndex } from '@/lib/content';
import '@/styles/docs.css';

export const metadata: Metadata = {
  title: {
    default: 'GoNext Documentation',
    template: '%s · GoNext Docs',
  },
  description: 'Documentation, ADRs, and architectural references for the GoNext platform.',
};

export default async function RootLayout({
  children,
}: {
  children: ReactNode;
}): Promise<ReactElement> {
  // The search index is built once per build and inlined into the layout
  // chunk. Keeps the index out of `_next/data` round-trips.
  const entries = await buildSearchIndex();
  return (
    <html lang="en">
      <body>
        <header className="docs-shell__header">
          <Link href="/" className="docs-shell__brand">GoNext</Link>
          <nav className="docs-shell__nav" aria-label="Primary">
            <Link href="/docs" className="docs-shell__nav-link">Docs</Link>
            <Link href="/adr" className="docs-shell__nav-link">ADRs</Link>
            <Link href="/api" className="docs-shell__nav-link">API Reference</Link>
            <Link href="/docs/proposals/14-proposals" className="docs-shell__nav-link">Proposals</Link>
          </nav>
          <SearchBar entries={entries} />
        </header>
        {children}
      </body>
    </html>
  );
}
