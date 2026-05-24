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
 */
import type { Metadata } from 'next';
import type { ReactElement, ReactNode } from 'react';
import './globals.css';

export const metadata: Metadata = {
  title: 'GoNext Admin',
  description: 'GoNext admin dashboard',
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
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
