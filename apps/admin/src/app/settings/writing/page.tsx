/**
 * Writing settings — server component (stub).
 *
 * Renders defaults for new content. The category/format option lists are
 * hard-coded for now; once Posts (#31) and Taxonomies (#32) land they'll
 * come from the API.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { fetchSettings } from '../api';
import { WritingForm } from './WritingForm';

export default async function WritingSettingsPage(): Promise<ReactElement> {
  const { values, available } = await fetchSettings('core.writing');
  return (
    <section>
      <p className="muted">
        <Link href="/settings">← Back to settings</Link>
      </p>
      <h1>Writing</h1>
      <p className="muted">
        Stub: defaults for new posts. Real category and format lists arrive
        with the Posts and Taxonomies issues.
      </p>
      <WritingForm
        initialValues={values}
        banner={available ? undefined : 'Settings API not available — values shown are defaults.'}
      />
    </section>
  );
}
