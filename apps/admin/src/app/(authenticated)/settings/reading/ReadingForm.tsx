'use client';

/**
 * Client wrapper for the Reading settings form. See `general/GeneralForm.tsx`
 * for the pattern.
 */
import type { ReactElement } from 'react';
import { SettingsForm } from '../SettingsForm';
import { patchSettings } from '../api';
import type { Setting, SettingsValues } from '../types';

export const READING_SCHEMA: readonly Setting[] = [
  {
    key: 'core.reading.posts_per_page',
    label: 'Posts per page',
    type: 'number',
    required: true,
    placeholder: '10',
    help: 'Number of posts shown on the blog index and feed.',
  },
  {
    key: 'core.reading.show_summary',
    label: 'Show post summary',
    type: 'boolean',
    help: 'When enabled, archive pages show an excerpt instead of the full post.',
  },
  {
    key: 'core.site.tagline',
    label: 'Tagline',
    type: 'text',
    help: 'Re-used from General — appears below the title in feeds and themes.',
  },
];

export interface ReadingFormProps {
  initialValues: SettingsValues;
  banner?: string;
}

export function ReadingForm({
  initialValues,
  banner,
}: ReadingFormProps): ReactElement {
  return (
    <SettingsForm
      schema={READING_SCHEMA}
      initialValues={initialValues}
      onSubmit={patchSettings}
      banner={banner}
    />
  );
}
