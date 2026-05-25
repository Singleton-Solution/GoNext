'use client';

/**
 * Client wrapper for the Writing settings form. Schema covers the three
 * sections the page wraps in paper-2 cards: per-post defaults, the
 * preferred editor (block vs classic), and post-by-email.
 */
import type { ReactElement } from 'react';
import { SettingsForm } from '../SettingsForm';
import { patchSettings } from '../api';
import type { Setting, SettingsSection, SettingsValues } from '../types';

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
  {
    key: 'core.writing.default_editor',
    label: 'Default editor',
    type: 'select',
    required: true,
    options: [
      { value: 'block', label: 'Block editor' },
      { value: 'classic', label: 'Classic editor' },
    ],
    help: 'Which editor opens when you click “New post”.',
  },
  {
    key: 'core.writing.post_by_email_enabled',
    label: 'Enable post by email',
    type: 'boolean',
    help: 'Messages sent to the address below are converted to drafts.',
  },
  {
    key: 'core.writing.post_by_email_address',
    label: 'Inbound email address',
    type: 'text',
    placeholder: 'posts@example.com',
    help: 'Only used when post-by-email is enabled.',
  },
];

export interface WritingFormProps {
  initialValues: SettingsValues;
  banner?: string;
  sections?: readonly SettingsSection[];
}

export function WritingForm({
  initialValues,
  banner,
  sections,
}: WritingFormProps): ReactElement {
  return (
    <SettingsForm
      schema={WRITING_SCHEMA}
      initialValues={initialValues}
      onSubmit={patchSettings}
      banner={banner}
      sections={sections}
    />
  );
}
