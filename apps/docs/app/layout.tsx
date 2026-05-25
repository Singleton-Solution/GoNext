/**
 * Root layout for @gonext/docs.
 *
 * Renders the persistent shell for every docs route: the topbar with
 * brand wordmark, primary nav, and global search; everything below the
 * topbar is owned by the route — sidebars, articles, and TOC come from
 * the per-section layouts under `app/docs/` and `app/adr/`.
 *
 * Brand fonts (Living-Systems handoff): Archivo for display headlines,
 * Geist for UI/body, Geist Mono for code, Instrument Serif for the
 * signature italic accents that swap in inside <em> tags. They load via
 * `next/font/google` so the URLs are self-hosted, layout shift is
 * suppressed by next/font's reserved-space metric, and the CSS
 * variables surface for both Tailwind utilities and raw selectors in
 * `styles/docs.css` / `src/styles/tokens.css`.
 */
import type { Metadata } from 'next';
import type { ReactElement, ReactNode } from 'react';
import Link from 'next/link';
import {
  Archivo,
  Geist,
  Geist_Mono,
  Instrument_Serif,
} from 'next/font/google';
import { SearchBar } from '@/components/SearchBar';
import { buildSearchIndex } from '@/lib/content';
import '@/styles/docs.css';

const archivo = Archivo({
  subsets: ['latin'],
  weight: ['500', '600', '700', '800', '900'],
  variable: '--font-display',
  display: 'swap',
});

const geist = Geist({
  subsets: ['latin'],
  weight: ['400', '500', '600', '700'],
  variable: '--font-sans',
  display: 'swap',
});

const geistMono = Geist_Mono({
  subsets: ['latin'],
  weight: ['400', '500'],
  variable: '--font-mono',
  display: 'swap',
});

const instrumentSerif = Instrument_Serif({
  subsets: ['latin'],
  weight: ['400'],
  style: ['normal', 'italic'],
  variable: '--font-serif',
  display: 'swap',
});

export const metadata: Metadata = {
  title: {
    default: 'GoNext Documentation',
    template: '%s · GoNext Docs',
  },
  description:
    'Documentation, ADRs, and architectural references for the GoNext platform — a modular CMS built on Go and Next.js.',
  icons: {
    icon: '/favicon.svg',
  },
};

export default async function RootLayout({
  children,
}: {
  children: ReactNode;
}): Promise<ReactElement> {
  // The search index is built once per build and inlined into the layout
  // chunk. Keeps the index out of `_next/data` round-trips.
  const entries = await buildSearchIndex();
  const fontVariables = [
    archivo.variable,
    geist.variable,
    geistMono.variable,
    instrumentSerif.variable,
  ].join(' ');
  return (
    <html lang="en" className={fontVariables}>
      <body>
        <header className="docs-topbar">
          <div className="docs-topbar__left">
            <Link href="/" className="docs-topbar__brand" aria-label="GoNext Docs home">
              <span className="wordmark">
                <span className="wm-go">Go</span>
                <span className="wm-next">Next</span>
              </span>
              <span className="docs-topbar__brand-tag">Docs</span>
            </Link>
            <nav className="docs-topbar__nav" aria-label="Primary">
              <Link href="/docs" className="docs-topbar__nav-link">Docs</Link>
              <Link href="/adr" className="docs-topbar__nav-link">ADRs</Link>
              <Link href="/api" className="docs-topbar__nav-link">API Reference</Link>
              <Link href="/docs/proposals/14-proposals" className="docs-topbar__nav-link">Proposals</Link>
            </nav>
          </div>
          <div className="docs-topbar__right">
            <SearchBar entries={entries} />
            <span className="docs-topbar__version">v1.0</span>
          </div>
        </header>
        {children}
      </body>
    </html>
  );
}
