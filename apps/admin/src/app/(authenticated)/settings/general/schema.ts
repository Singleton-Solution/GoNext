/**
 * Schema for the General settings form.
 *
 * Mirrors the `core.site` + core identity keys exposed by the settings
 * registry (#325). Lives in its own module so the test can import the same
 * array the page renders.
 */
import type { Setting } from '../types';

export const GENERAL_SCHEMA: readonly Setting[] = [
  {
    key: 'core.site.name',
    label: 'Site name',
    type: 'text',
    required: true,
    placeholder: 'GoNext',
    help: 'Shown in the browser tab, RSS feed, and email notifications.',
  },
  {
    key: 'core.site.tagline',
    label: 'Tagline',
    type: 'text',
    placeholder: 'Just another GoNext site',
    help: 'A short description of your site — appears below the title in themes that use it.',
  },
  {
    key: 'core.site.url',
    label: 'Site URL',
    type: 'url',
    required: true,
    placeholder: 'https://example.com',
    help: 'The canonical address used in feeds, emails, and SEO metadata.',
  },
  {
    key: 'core.timezone',
    label: 'Timezone',
    type: 'select',
    required: true,
    options: [
      { value: 'UTC', label: 'UTC' },
      { value: 'America/New_York', label: 'America/New_York' },
      { value: 'America/Los_Angeles', label: 'America/Los_Angeles' },
      { value: 'Europe/London', label: 'Europe/London' },
      { value: 'Europe/Paris', label: 'Europe/Paris' },
      { value: 'Asia/Tokyo', label: 'Asia/Tokyo' },
    ],
  },
  {
    key: 'core.locale',
    label: 'Site language',
    type: 'select',
    required: true,
    options: [
      { value: 'en-US', label: 'English (United States)' },
      { value: 'en-GB', label: 'English (United Kingdom)' },
      { value: 'fr-FR', label: 'French' },
      { value: 'de-DE', label: 'German' },
      { value: 'es-ES', label: 'Spanish' },
      { value: 'ja-JP', label: 'Japanese' },
    ],
  },
];
