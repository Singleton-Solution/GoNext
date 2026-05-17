/**
 * General settings — server component.
 *
 * Fetches the current `core.site` values from the registry on the server so
 * the form pre-fills on first paint. If the registry endpoint is unreachable
 * (it ships behind a feature flag while #325 is still wiring through the
 * gateway), we render the form with empty values and a banner explaining
 * the situation.
 *
 * The actual interactive form lives in `GeneralForm` (a client component) to
 * keep the server bundle slim.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { fetchSettings } from '../api';
import { GeneralForm } from './GeneralForm';

export default async function GeneralSettingsPage(): Promise<ReactElement> {
  const { values, available } = await fetchSettings('core.site');
  return (
    <section>
      <p className="muted">
        <Link href="/settings">← Back to settings</Link>
      </p>
      <h1>General</h1>
      <p className="muted">Identity and locale for this site.</p>
      <GeneralForm
        initialValues={values}
        banner={available ? undefined : 'Settings API not available — values shown are defaults.'}
      />
    </section>
  );
}
