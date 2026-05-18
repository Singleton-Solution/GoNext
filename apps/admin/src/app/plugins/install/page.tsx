/**
 * Plugin install — entry screen.
 *
 * Renders the install form (`InstallForm`). The form drives the
 * upload UI, manifest preview, and the capability review. The server
 * action lives in `../actions.ts`.
 *
 * This is intentionally a thin server component; all interactive state
 * lives in the client `InstallForm`. The page exists to give the route
 * its own URL (`/plugins/install`) and to hang the install header and
 * back link off something stable.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { InstallForm } from './InstallForm';

export const dynamic = 'force-dynamic';

export default function InstallPluginPage(): ReactElement {
  return (
    <section>
      <p style={{ marginBottom: 12 }}>
        <Link href="/plugins">← Back to plugins</Link>
      </p>
      <h1 style={{ marginTop: 0, fontSize: 22, fontWeight: 600 }}>
        Install plugin
      </h1>
      <p
        style={{
          margin: '4px 0 20px',
          color: 'var(--color-text-muted, #6b7280)',
          fontSize: 14,
          maxWidth: 640,
        }}
      >
        Upload a <code>.gnplugin</code> bundle or paste a <code>manifest.json</code>{' '}
        to preview the plugin. The host will validate the manifest and reject
        anything that doesn’t conform; you’ll see the capability review below
        before the install request is fired.
      </p>
      <InstallForm />
    </section>
  );
}
