/**
 * Settings overview — landing page that funnels into each group.
 *
 * Renders four cards (General, Reading, Writing, Permalinks) matching the
 * groups exposed by the backend settings registry (#325). Each card links
 * to its sub-route where the actual schema-driven form lives.
 *
 * The card grid reuses the dashboard's `widget-grid` styling so the admin
 * stays visually coherent before the design-system extraction (#34) lands.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';

type SettingsCard = {
  href: string;
  title: string;
  body: string;
};

const CARDS: readonly SettingsCard[] = [
  {
    href: '/settings/general',
    title: 'General',
    body: 'Site name, tagline, URL, timezone, and locale.',
  },
  {
    href: '/settings/reading',
    title: 'Reading',
    body: 'Posts per page and front-end display preferences.',
  },
  {
    href: '/settings/writing',
    title: 'Writing',
    body: 'Default category and post format for new content.',
  },
  {
    href: '/settings/permalinks',
    title: 'Permalinks',
    body: 'URL structure for posts and pages.',
  },
  {
    href: '/settings/account',
    title: 'Account',
    body: 'Passkeys, password, and sign-in security.',
  },
];

export default function SettingsOverviewPage(): ReactElement {
  return (
    <section>
      <h1>Settings</h1>
      <p className="muted">
        Configure how this GoNext site behaves. Pick a group below to edit it.
      </p>

      <div className="widget-grid" data-testid="settings-overview-grid">
        {CARDS.map((card) => (
          <Link
            key={card.href}
            href={card.href}
            className="widget settings-card"
            aria-label={`${card.title} settings`}
          >
            <h2 className="widget__title">{card.title}</h2>
            <p className="widget__body">{card.body}</p>
          </Link>
        ))}
      </div>
    </section>
  );
}
