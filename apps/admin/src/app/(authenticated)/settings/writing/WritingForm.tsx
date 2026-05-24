'use client';

/**
 * Client wrapper for the Writing settings form.
 */
import type { ReactElement } from 'react';
import { SettingsForm } from '../SettingsForm';
import { patchSettings } from '../api';
import type { Setting, SettingsValues } from '../types';

export const WRITING_SCHEMA: readonly Setting[] = [
  {
    key: 'core.writing.default_category',
    label: 'Default category',
    type: 'select',
    required: true,
    options: [
      { value: 'uncategorized', label: 'Uncategorized' },
      { value: 'news', label: 'News' },
      { value: 'blog', label: 'Blog' },
      { value: 'updates', label: 'Updates' },
    ],
    help: 'Applied to new posts that do not explicitly set one.',
  },
  {
    key: 'core.writing.default_format',
    label: 'Default post format',
    type: 'select',
    required: true,
    options: [
      { value: 'standard', label: 'Standard' },
      { value: 'aside', label: 'Aside' },
      { value: 'gallery', label: 'Gallery' },
      { value: 'link', label: 'Link' },
      { value: 'quote', label: 'Quote' },
    ],
    help: 'Themes use this hint to pick a post template.',
  },
];

export interface WritingFormProps {
  initialValues: SettingsValues;
  banner?: string;
}

export function WritingForm({
  initialValues,
  banner,
}: WritingFormProps): ReactElement {
  return (
    <SettingsForm
      schema={WRITING_SCHEMA}
      initialValues={initialValues}
      onSubmit={patchSettings}
      banner={banner}
    />
  );
}
