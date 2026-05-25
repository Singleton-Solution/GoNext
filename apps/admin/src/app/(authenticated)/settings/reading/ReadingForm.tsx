'use client';

/**
 * Client wrapper for the Reading settings form. See `general/GeneralForm.tsx`
 * for the pattern.
 *
 * Schema covers the four sections the page wraps in paper-2 cards:
 * Homepage (type + selected page), Blog index (per-page count + summary
 * toggle), RSS (item count + full-text), and the re-used Tagline from
 * General.
 */
import type { ReactElement } from 'react';
import { SettingsForm } from '../SettingsForm';
import { patchSettings } from '../api';
import type { Setting, SettingsSection, SettingsValues } from '../types';

export const READING_SCHEMA: readonly Setting[] = [
  {
    key: 'core.reading.homepage_type',
    label: 'Homepage shows',
    type: 'select',
    required: true,
    options: [
      { value: 'latest_posts', label: 'Latest posts' },
      { value: 'static_page', label: 'A static page' },
    ],
    help: 'Pick whether the homepage is the blog index or a designated page.',
  },
  {
    key: 'core.reading.homepage_page_id',
    label: 'Static homepage slug',
    type: 'text',
    placeholder: 'welcome',
    help: 'Only used when “A static page” is selected above.',
  },
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
    key: 'core.reading.rss_items',
    label: 'Items in RSS feed',
    type: 'number',
    placeholder: '10',
    help: 'Number of entries served at /feed/.',
  },
  {
    key: 'core.reading.rss_full_text',
    label: 'Include full text in RSS',
    type: 'boolean',
    help: 'Disable to serve excerpts only — useful if your full posts are long.',
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
  sections?: readonly SettingsSection[];
}

export function ReadingForm({
  initialValues,
  banner,
  sections,
}: ReadingFormProps): ReactElement {
  return (
    <SettingsForm
      schema={READING_SCHEMA}
      initialValues={initialValues}
      onSubmit={patchSettings}
      banner={banner}
      sections={sections}
    />
  );
}
