/**
 * Create a new redirect rule.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { RedirectForm } from '../RedirectForm';

export const dynamic = 'force-dynamic';

export default function NewRedirectPage(): ReactElement {
  return (
    <section>
      <p>
        <Link href="/redirects">&larr; Redirects</Link>
      </p>
      <h1>New redirect</h1>
      <p className="muted">
        Decide whether the source is a literal path (default) or a regular
        expression, pick the HTTP status, and save. Literal rules match in
        O(1); regex rules cost slightly more per request but support
        capture-group substitution.
      </p>
      <RedirectForm />
    </section>
  );
}
