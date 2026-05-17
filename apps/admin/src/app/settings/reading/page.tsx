/**
 * Reading settings — server component.
 *
 * Currently a stub: the registry surface for `core.reading` is in flight.
 * The form renders with empty values plus the `tagline` re-used from
 * `core.site` so editors can do a single-pass review without bouncing
 * between groups. Once the reading-side of the registry lands we'll drop
 * the banner and the `tagline` re-use becomes redundant.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { fetchSettings } from '../api';
import { ReadingForm } from './ReadingForm';

export default async function ReadingSettingsPage(): Promise<ReactElement> {
  // We fetch both groups so the re-used `tagline` field pre-fills from the
  // canonical `core.site` value, not a stale copy.
  const [reading, site] = await Promise.all([
    fetchSettings('core.reading'),
    fetchSettings('core.site'),
  ]);

  const initialValues = {
    ...reading.values,
    'core.site.tagline': site.values['core.site.tagline'],
  };

  const available = reading.available && site.available;
  return (
    <section>
      <p className="muted">
        <Link href="/settings">← Back to settings</Link>
      </p>
      <h1>Reading</h1>
      <p className="muted">
        Stub: the reading registry ships behind a flag. Edits will save once
        the API is live.
      </p>
      <ReadingForm
        initialValues={initialValues}
        banner={available ? undefined : 'Settings API not available — values shown are defaults.'}
      />
    </section>
  );
}
