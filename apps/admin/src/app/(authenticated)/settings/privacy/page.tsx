/**
 * Privacy settings — issue #225.
 *
 * Fetches the current privacy-group values from the registry on the
 * server so the form pre-fills on first paint. Mirrors the general /
 * reading / writing pages structurally.
 *
 * The GDPR self-service toggle on this page gates the public
 * /api/v1/account/data/export endpoint — flipping it off makes the
 * endpoint return 403 and the user-facing affordance disappear.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';

import { Headline } from '@/components/ui/headline';

import { fetchSettings } from '../api';
import type { SettingsSection } from '../types';
import { PrivacyForm } from './PrivacyForm';

export const dynamic = 'force-dynamic';

const SECTIONS: readonly SettingsSection[] = [
  {
    title: 'Cookie policy',
    description: "What the site tells visitors about its use of cookies.",
    keys: [
      'core.privacy.cookie_policy_url',
      'core.privacy.cookie_policy_text',
    ],
  },
  {
    title: 'Retention windows',
    description:
      "How long the platform keeps audit, session, and login records. Use 0 to retain indefinitely.",
    keys: [
      'core.privacy.retention.audit_days',
      'core.privacy.retention.sessions_days',
      'core.privacy.retention.login_attempts_days',
    ],
  },
  {
    title: 'GDPR self-service',
    description:
      "Allow signed-in users to download a JSON archive of their personal data. Disabling this returns 403 from /api/v1/account/data/export.",
    keys: ['core.privacy.allow_gdpr_self_service'],
  },
];

export default async function PrivacySettingsPage(): Promise<ReactElement> {
  const { values, available } = await fetchSettings('privacy');
  return (
    <section className="settings-page">
      <Link href="/settings" className="settings-page__back">
        ← Back to settings
      </Link>
      <header className="settings-page__head">
        <Headline as="h1" size="sub">
          Privacy <em>settings</em>.
        </Headline>
        <p className="settings-page__lead">
          Cookies, retention windows, and the GDPR self-service toggle.
        </p>
      </header>
      <PrivacyForm
        initialValues={values}
        banner={
          available
            ? undefined
            : "Settings registry isn't reachable — defaults shown. Save to retry."
        }
        sections={SECTIONS}
      />
    </section>
  );
}
