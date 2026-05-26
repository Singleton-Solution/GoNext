/**
 * Theme installer page — `/appearance/install` (issue #13).
 *
 * Standalone landing for the `.gntheme` upload form. The umbrella
 * `/appearance/themes` page (issue #65) embeds the same drop zone
 * for operators who want install + switch on one screen; this route
 * exists for the deep-link case ("send me the install URL") and as
 * the destination of the "Install a theme" CTA in the customizer
 * sidebar.
 */

import type { ReactElement } from 'react';
import Link from 'next/link';
import { ArrowLeft } from 'lucide-react';
import { Headline } from '@/components/ui/headline';
import { InstallerClient } from './InstallerClient';

export const dynamic = 'force-dynamic';

export default function InstallPage(): ReactElement {
  return (
    <section
      aria-labelledby="install-heading"
      data-testid="install-page"
      className="flex flex-col gap-10 pb-16"
    >
      <header className="flex flex-col gap-3 border-b border-border pb-8">
        <Link
          href="/appearance/themes"
          className="inline-flex items-center gap-1 font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep hover:text-emerald"
        >
          <ArrowLeft aria-hidden className="h-3 w-3" /> Back to themes
        </Link>
        <Headline as="h1" size="page" id="install-heading">
          Install a <em>theme</em>.
        </Headline>
        <p className="max-w-[640px] text-md leading-normal text-fg-muted">
          Drop a <code className="font-mono text-sm">.gntheme</code> archive below. The installer
          validates the manifest, refuses path-traversal entries, and writes atomically — a
          half-installed theme can&apos;t happen.
        </p>
      </header>
      <InstallerClient />
    </section>
  );
}
