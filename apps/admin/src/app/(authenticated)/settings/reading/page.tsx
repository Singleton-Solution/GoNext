/**
 * Reading settings — server component.
 *
 * Currently a stub: the registry surface for `core.reading` is in flight.
 * The form renders with empty values plus the `tagline` re-used from
 * `core.site` so editors can do a single-pass review without bouncing
 * between groups. Once the reading-side of the registry lands we'll drop
 * the banner and the `tagline` re-use becomes redundant.
 *
 * Visual treatment matches `/settings/general/` — paper-2 section cards
 * carry the form, the Archivo display headline uses the italic-accent
 * rule, and the save action is the emerald `<Button>` per the
 * Living-Systems brand (PR #432).
 */
import type { ReactElement } from 'react';
import Link from 'next/link';

import { Headline } from '@/components/ui/headline';

import { fetchSettings } from '../api';
import type { SettingsSection } from '../types';
import { ReadingForm } from './ReadingForm';

export const dynamic = 'force-dynamic';

const SECTIONS: readonly SettingsSection[] = [
  {
    title: 'Homepage',
    description: 'What visitors see at the root of your site.',
    keys: ['core.reading.homepage_type', 'core.reading.homepage_page_id'],
  },
  {
    title: 'Blog index',
    description: 'How posts are paginated on archive pages.',
    keys: ['core.reading.posts_per_page', 'core.reading.show_summary'],
  },
  {
    title: 'RSS',
    description: 'Feed shape served at `/feed/`.',
    keys: ['core.reading.rss_items', 'core.reading.rss_full_text'],
  },
  {
    title: 'Tagline',
    description: 'Re-used from General — shown in feeds and themes.',
    keys: ['core.site.tagline'],
  },
];

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
    <section className="settings-page">
      <div aria-hidden="true" className="settings-page__glow settings-page__glow--emerald" />
      <div aria-hidden="true" className="settings-page__glow settings-page__glow--lavender" />

      <Link href="/settings" className="settings-page__back">
        ← Back to settings
      </Link>
      <header className="settings-page__head">
        <Headline as="h1" size="sub">
          Reading <em>settings</em>.
        </Headline>
        <p className="settings-page__lead">
          Pagination, homepage layout, and RSS shape. The registry that backs
          these values ships behind a flag — edits will persist once the API
          is live.
        </p>
      </header>

      <ReadingForm
        initialValues={initialValues}
        sections={SECTIONS}
        banner={available ? undefined : 'Settings API not available — values shown are defaults.'}
      />
    </section>
  );
}
