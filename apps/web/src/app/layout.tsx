/**
 * Root layout for @gonext/web.
 *
 * The public site is intentionally minimal at the layout level — the
 * theme's header/footer parts wrap the content inside each page
 * component, not here. The layout only owns:
 *
 *   1. The `<html>` / `<body>` envelope.
 *   2. Default `<head>` metadata that applies to every page (charset,
 *      viewport, robots default). Per-page overrides come from each
 *      route's `generateMetadata`.
 *   3. The baseline stylesheet (`./globals.css`). Theme-emitted CSS
 *      custom properties are injected per-page via the
 *      `<style data-gn-theme>` tag the page components render — this
 *      lets us swap themes without invalidating the layout's cached
 *      HTML envelope.
 *
 * Robots default: `index, follow`. Production sites typically gate
 * with a reverse-proxy header for staging/preview environments; this
 * default mirrors what a public CMS expects out of the box.
 */
import type { Metadata } from 'next';
import type { ReactElement, ReactNode } from 'react';
import './globals.css';

export const metadata: Metadata = {
  title: {
    default: 'GoNext',
    template: '%s · GoNext',
  },
  description: 'A site powered by GoNext.',
  robots: {
    index: true,
    follow: true,
  },
};

export default function RootLayout({
  children,
}: {
  children: ReactNode;
}): ReactElement {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
