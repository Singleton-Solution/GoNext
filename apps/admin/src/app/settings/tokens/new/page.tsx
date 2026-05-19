/**
 * /settings/tokens/new — issue a new Personal Access Token.
 *
 * Server-rendered shell wrapping the interactive client component. The
 * shell stays trivial so the route compiles into a tiny initial payload
 * and the JS bundle only flips on for the form itself.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { NewTokenFlow } from './NewTokenFlow';

export default function NewTokenPage(): ReactElement {
  return (
    <section>
      <p className="muted">
        <Link href="/settings/tokens">← Back to tokens</Link>
      </p>
      <h1>Create a personal access token</h1>
      <p className="muted">
        Tokens are valid for the scopes you select, intersected with your
        own capabilities. Treat them as passwords. You’ll see the plaintext
        exactly once on the next screen.
      </p>
      <NewTokenFlow />
    </section>
  );
}
