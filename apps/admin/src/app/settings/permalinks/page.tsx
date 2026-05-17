/**
 * Permalinks settings — server component.
 *
 * One text field for the URL format string plus a live preview rendered
 * inside the form (see `PermalinksForm` for the preview logic).
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { fetchSettings } from '../api';
import { PermalinksForm } from './PermalinksForm';

export default async function PermalinksSettingsPage(): Promise<ReactElement> {
  const { values, available } = await fetchSettings('core.permalinks');
  return (
    <section>
      <p className="muted">
        <Link href="/settings">← Back to settings</Link>
      </p>
      <h1>Permalinks</h1>
      <p className="muted">URL structure for posts. Tokens are filled at render time.</p>
      <PermalinksForm
        initialValues={values}
        banner={available ? undefined : 'Settings API not available — values shown are defaults.'}
      />
    </section>
  );
}
