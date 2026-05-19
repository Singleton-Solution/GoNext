/**
 * /settings/tokens — list of the current user's Personal Access Tokens.
 *
 * The list itself loads on the client (the data is per-user, the SSR
 * pass has no session context, and the page is rarely visited so the
 * extra round-trip is unobjectionable). The server component below is
 * purely the page chrome.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { TokensList } from './components/TokensList';

export default function TokensPage(): ReactElement {
  return (
    <section>
      <p className="muted">
        <Link href="/settings">← Back to settings</Link>
      </p>
      <h1>Personal access tokens</h1>
      <p className="muted">
        Long-lived bearer tokens for CI jobs, the CLI, and external scripts.
        Each token carries an explicit set of scopes, intersected with your
        own capabilities at every request. Revoke any token from this page;
        a revoked token can never be reactivated.
      </p>

      <div className="tokens-actions">
        <Link href="/settings/tokens/new" className="btn btn-primary" data-testid="tokens-new-cta">
          Create token
        </Link>
      </div>

      <TokensList />
    </section>
  );
}
