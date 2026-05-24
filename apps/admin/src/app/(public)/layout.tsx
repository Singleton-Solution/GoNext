/**
 * Layout for the `(public)` route group.
 *
 * Wraps unauthenticated surfaces — `/login`, `/setup`, and any future
 * forgot-password / verify-email companions — in a bare, centered
 * shell. Critically, this layout does NOT render the admin sidebar or
 * header: a visitor reaching /login has no session yet and showing
 * signed-in IA would both leak the app structure and produce an
 * inconsistent first-run experience.
 *
 * The shell is intentionally minimal:
 *   - full-viewport flex container, centered card
 *   - inherits global typography and color tokens from globals.css
 *   - no nav, no header — just a `<main>` for the page content
 *
 * Existing per-surface styles (`.login-card`, `.setup`, …) already
 * compose with this container; the wrapper here just supplies the
 * outer centering chrome.
 */
import type { ReactElement, ReactNode } from 'react';

export default function PublicLayout({
  children,
}: {
  children: ReactNode;
}): ReactElement {
  return (
    <div className="public-shell">
      <main className="public-shell__content">{children}</main>
    </div>
  );
}
