/**
 * Theme Customizer — server component entry.
 *
 * Spec: docs/03-theme-system.md §3.5 + issue #355.
 *
 * Architecture
 * ============
 * Server component does the initial fetch and hands off to the
 * client island (`CustomizerClient`). The split keeps the first paint
 * fast (no client-side fetch round trip to draw the form) while still
 * giving us a fully-interactive editor.
 *
 * Error handling
 * ==============
 * On any failure we render a banner instead of throwing — the
 * customizer is a secondary surface and crashing the layout for an
 * API outage would be a worse experience than a degraded form.
 */
import type { ReactElement } from 'react';
import { fetchActiveServer } from './api-server';
import { CustomizerClient } from './CustomizerClient';
import './customizer.css';

const DEFAULT_PUBLIC_SITE_URL = 'http://localhost:3000';

export default async function CustomizerPage(): Promise<ReactElement> {
  const result = await fetchActiveServer();
  const publicSiteUrl =
    (typeof process !== 'undefined' && process.env.NEXT_PUBLIC_SITE_URL) ||
    DEFAULT_PUBLIC_SITE_URL;

  if (!result.available) {
    return (
      <section>
        <h1
          style={{
            fontFamily: 'var(--font-display)',
            fontSize: 'var(--t-3xl)',
            fontWeight: 800,
            letterSpacing: 'var(--track-tight)',
            color: 'var(--ink)',
            margin: 0,
            lineHeight: 'var(--lh-tight)',
          }}
        >
          Customize your{' '}
          <em
            style={{
              fontFamily: 'var(--font-serif)',
              fontStyle: 'italic',
              fontWeight: 400,
              color: 'var(--emerald-deep)',
              fontSize: '1.05em',
            }}
          >
            site
          </em>
          .
        </h1>
        <p className="customizer__banner" role="status">
          Theme customizer is unavailable: {result.error}
        </p>
      </section>
    );
  }

  return (
    <CustomizerClient active={result.data} publicSiteUrl={publicSiteUrl} />
  );
}
