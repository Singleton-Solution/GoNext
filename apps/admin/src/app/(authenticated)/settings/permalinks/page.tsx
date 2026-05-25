/**
 * Permalinks settings — server component.
 *
 * One text field for the URL format string plus a live preview rendered
 * inside the form (see `PermalinksForm` for the preview logic). The page
 * also exposes a set of preset radio cards above the custom-structure
 * input so editors can pick a common format without typing tokens by hand.
 *
 * Visual treatment matches the rest of `/settings/*` — paper-2 section
 * cards, italic-accent display headline, paper-3 radio cards for the
 * presets with a mono URL preview each, emerald save action (PR #432).
 */
import type { ReactElement } from 'react';
import Link from 'next/link';

import { Headline } from '@/components/ui/headline';

import { fetchSettings } from '../api';
import { PermalinksForm } from './PermalinksForm';

export const dynamic = 'force-dynamic';

export default async function PermalinksSettingsPage(): Promise<ReactElement> {
  const { values, available } = await fetchSettings('core.permalinks');
  return (
    <section className="settings-page">
      <div aria-hidden="true" className="settings-page__glow settings-page__glow--emerald" />
      <div aria-hidden="true" className="settings-page__glow settings-page__glow--lavender" />

      <Link href="/settings" className="settings-page__back">
        ← Back to settings
      </Link>
      <header className="settings-page__head">
        <Headline as="h1" size="sub">
          Permalink <em>structure</em>.
        </Headline>
        <p className="settings-page__lead">
          URL structure for posts. Pick a preset or compose your own —
          tokens are filled at render time.
        </p>
      </header>

      <PermalinksForm
        initialValues={values}
        banner={available ? undefined : 'Settings API not available — values shown are defaults.'}
      />
    </section>
  );
}
