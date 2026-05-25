/**
 * Root layout for @gonext/admin.
 *
 * The root layout is intentionally minimal: it owns the `<html>` and
 * `<body>` shell, the global stylesheet, and the document-level
 * metadata. It does NOT render any chrome (sidebar, header, etc.) so
 * unauthenticated surfaces — the login screen, the first-run setup
 * wizard — can render as bare centered cards without leaking signed-in
 * UI to visitors who have no session.
 *
 * Chrome lives in the nested layouts under the route groups:
 *
 *   - `(public)/layout.tsx`        — bare centered shell for /login,
 *                                    /setup, /forgot-password, etc.
 *   - `(authenticated)/layout.tsx` — sidebar + header for the signed-in
 *                                    app surfaces (dashboard, posts,
 *                                    settings, …). Route-level auth gating
 *                                    is enforced server-side in
 *                                    `apps/admin/middleware.ts`; the
 *                                    layout itself does not redirect so
 *                                    the gate can stay in one place.
 *
 * The split was made when issue #(this PR) surfaced that the previous
 * scaffold rendered the sidebar on /login and /setup, which both leaks
 * signed-in IA to visitors and produces a visually broken first-run
 * experience.
 *
 * Brand fonts (Living-Systems handoff): Archivo for display headlines,
 * Geist for UI/body, Geist Mono for code, and Instrument Serif for the
 * signature italic accents that swap in inside <em> tags. They are
 * loaded via `next/font/google` so the URLs are self-hosted, layout
 * shift is suppressed by next/font's reserved-space metric, and the
 * CSS variables surface for both Tailwind utilities and raw CSS
 * selectors in `globals.css` / `tokens.css`.
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
  title: 'GoNext Admin',
  description: 'GoNext admin dashboard',
  icons: {
    icon: '/favicon.svg',
  },
  // The Dockerfile / reverse proxy applies real robots controls; this is a
  // belt-and-braces default for the standalone dev server.
  robots: {
    index: false,
    follow: false,
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
      <body>{children}</body>
    </html>
  );
}
