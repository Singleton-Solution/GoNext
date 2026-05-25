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
 *
 * Visual treatment follows the Living-Systems brand foundation (PR #432):
 *   • Archivo display headline with the italic-accent rule
 *     ("General <em>settings</em>.")
 *   • Fields split across four paper-2 section cards (Site identity, Locale,
 *     Timezone, Default role) so the form has the same visual rhythm as the
 *     restyled customizer page.
 *   • Soft off-canvas emerald + lavender glows mirror the public-surface
 *     treatment without overwhelming the form.
 *
 * `dynamic = 'force-dynamic'` keeps the registry round-trip on every request
 * so editors never see a stale cached page after saving.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';

import { Headline } from '@/components/ui/headline';

import { fetchSettings } from '../api';
import type { SettingsSection } from '../types';
import { GeneralForm } from './GeneralForm';

export const dynamic = 'force-dynamic';

const SECTIONS: readonly SettingsSection[] = [
  {
    title: 'Site identity',
    description: 'How the world sees this GoNext install.',
    keys: ['core.site.name', 'core.site.tagline', 'core.site.url'],
  },
  {
    title: 'Locale',
    description: 'Language used across the editor and themes.',
    keys: ['core.locale'],
  },
  {
    title: 'Timezone',
    description: 'Used for scheduled posts and feed timestamps.',
    keys: ['core.timezone'],
  },
  {
    title: 'Default role',
    description: 'Applied to new users when no role is specified.',
    keys: ['core.default_role'],
  },
];

export default async function GeneralSettingsPage(): Promise<ReactElement> {
  const { values, available } = await fetchSettings('core.site');
  return (
    <section className="settings-page">
      <div aria-hidden="true" className="settings-page__glow settings-page__glow--emerald" />
      <div aria-hidden="true" className="settings-page__glow settings-page__glow--lavender" />

      <Link href="/settings" className="settings-page__back">
        ← Back to settings
      </Link>
      <header className="settings-page__head">
        <Headline as="h1" size="sub">
          General <em>settings</em>.
        </Headline>
        <p className="settings-page__lead">
          Identity and locale for this site. Changes apply across the editor,
          the public theme, and the RSS feed.
        </p>
      </header>

      <GeneralForm
        initialValues={values}
        sections={SECTIONS}
        banner={available ? undefined : 'Settings API not available — values shown are defaults.'}
      />
    </section>
  );
}
