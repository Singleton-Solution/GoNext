/**
 * Writing settings — server component (stub).
 *
 * Renders defaults for new content. The category/format option lists are
 * hard-coded for now; once Posts (#31) and Taxonomies (#32) land they'll
 * come from the API.
 *
 * Visual treatment matches the rest of `/settings/*` — paper-2 section
 * cards, italic-accent display headline, emerald save action (PR #432).
 */
import type { ReactElement } from 'react';
import Link from 'next/link';

import { Headline } from '@/components/ui/headline';

import { fetchSettings } from '../api';
import type { SettingsSection } from '../types';
import { WritingForm } from './WritingForm';

export const dynamic = 'force-dynamic';

const SECTIONS: readonly SettingsSection[] = [
  {
    title: 'Defaults',
    description: 'Applied to new posts when no value is provided.',
    keys: [
      'core.writing.default_category',
      'core.writing.default_format',
    ],
  },
  {
    title: 'Editor',
    description: 'How the “New post” button behaves.',
    keys: ['core.writing.default_editor'],
  },
  {
    title: 'Post by email',
    description: 'Inbound mailbox that converts incoming messages to drafts.',
    keys: [
      'core.writing.post_by_email_enabled',
      'core.writing.post_by_email_address',
    ],
  },
];

export default async function WritingSettingsPage(): Promise<ReactElement> {
  const { values, available } = await fetchSettings('core.writing');
  return (
    <section className="settings-page">
      <div aria-hidden="true" className="settings-page__glow settings-page__glow--emerald" />
      <div aria-hidden="true" className="settings-page__glow settings-page__glow--lavender" />

      <Link href="/settings" className="settings-page__back">
        ← Back to settings
      </Link>
      <header className="settings-page__head">
        <Headline as="h1" size="sub">
          Writing <em>settings</em>.
        </Headline>
        <p className="settings-page__lead">
          Defaults for new posts. Real category and format lists arrive with
          the Posts and Taxonomies issues.
        </p>
      </header>

      <WritingForm
        initialValues={values}
        sections={SECTIONS}
        banner={available ? undefined : 'Settings API not available — values shown are defaults.'}
      />
    </section>
  );
}
