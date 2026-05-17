/**
 * Root layout for @gonext/admin.
 *
 * Wraps every route in the admin shell: sidebar + main pane with a top
 * header. Children render into the `<main>` content area.
 */
import type { Metadata } from 'next';
import type { ReactElement, ReactNode } from 'react';
import { Sidebar } from './(components)/Sidebar';
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
      <body>
        <div className="app-shell">
          <Sidebar />
          <div className="app-shell__main">
            <header className="app-shell__header">GoNext Admin</header>
            <main className="app-shell__content">{children}</main>
          </div>
        </div>
      </body>
    </html>
  );
}
