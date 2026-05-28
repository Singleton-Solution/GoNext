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
 *   4. The four brand font families self-hosted via `next/font/google`.
 *      The CSS variables (--font-display, --font-sans, --font-mono,
 *      --font-serif) land on the <html> element so both Tailwind
 *      utilities and the raw `tokens.css` selectors resolve them at
 *      runtime. Mirrors apps/admin/src/app/layout.tsx so the brand
 *      surface stays in lock-step across the admin and the public
 *      marketing site.
 *
 * Robots default: `index, follow`. Production sites typically gate
 * with a reverse-proxy header for staging/preview environments; this
 * default mirrors what a public CMS expects out of the box.
 */
import type { Metadata } from 'next';
import type { ReactElement, ReactNode } from 'react';
import {
  Archivo,
  Geist,
  Geist_Mono,
  Instrument_Serif,
} from 'next/font/google';
import './globals.css';

// Public-site API base — same env var the typed client in src/lib/api.ts
// reads. We resolve it at module init (the value is baked into the
// build because NEXT_PUBLIC_* is browser-visible) so the <link> tag we
// inject in <head> renders with the right absolute URL. The link
// preloads the active theme's CSS so the renderer no longer paints
// with stock globals.css defaults — the API now serves
// /themes/active/style.css (issue #501).
const PUBLIC_API_URL: string =
  (typeof process !== 'undefined' && process.env.NEXT_PUBLIC_API_URL) ||
  'http://localhost:8080';

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
    default: 'GoNext — sites that live and grow',
    template: '%s · GoNext',
  },
  description:
    'An all-in-one alternative to WordPress — content, hosting, and commerce in one product. Built on Go and Next.js.',
  icons: {
    icon: '/favicon.svg',
  },
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
  const fontVariables = [
    archivo.variable,
    geist.variable,
    geistMono.variable,
    instrumentSerif.variable,
  ].join(' ');

  return (
    <html lang="en" className={fontVariables}>
      <head>
        {/*
         * Active-theme CSS preload (issue #501). The API serves the
         * theme's static stylesheet under /themes/active/style.css —
         * the "active" sentinel resolves through the core.active_theme
         * options row, so switching the active theme transparently
         * swaps the bytes returned here.
         */}
        <link
          rel="stylesheet"
          href={`${PUBLIC_API_URL}/themes/active/style.css`}
        />
      </head>
      <body>{children}</body>
    </html>
  );
}
