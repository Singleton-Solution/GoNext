/**
 * Layout for the `(authenticated)` route group.
 *
 * Renders the sidebar + main pane chrome that every signed-in admin
 * surface inherits. The route group itself doesn't add a URL segment,
 * so `/posts` still resolves to `(authenticated)/posts/page.tsx` —
 * but the layout boundary keeps the chrome scoped to authenticated
 * routes only.
 *
 * Auth gating is enforced edge-side in `apps/admin/middleware.ts`.
 * Any visitor under this group without a session cookie is redirected
 * to /login before the layout ever renders, so we don't need a
 * client-side guard here. Keeping the redirect in one place avoids
 * the double-redirect / flash patterns that bedevil mixed client/server
 * auth gates.
 *
 * Brand treatment ("Living systems"): the chrome is a forest-dark
 * sidebar paired with a cream paper main pane. The top header
 * carries the GoNext wordmark — its italic "Next" inherits the
 * brand's signature serif-italic accent.
 */
import type { ReactElement, ReactNode } from 'react';
import { Sidebar } from './_components/Sidebar';
import { TopHeader } from './_components/TopHeader';

export default function AuthenticatedLayout({
  children,
}: {
  children: ReactNode;
}): ReactElement {
  return (
    <div className="app-shell">
      <Sidebar />
      <div className="app-shell__main">
        <TopHeader />
        <main className="app-shell__content">{children}</main>
      </div>
    </div>
  );
}
