/**
 * /settings/privacy — GDPR self-service surface (issue #216).
 *
 * Two actions, both irreversible to varying degrees:
 *
 *   1. Download your data — kicks off an async export job. The
 *      worker assembles a ZIP and returns a download URL through the
 *      poll endpoint; this page surfaces the job id and shows a
 *      banner with the polling URL.
 *
 *   2. Delete account — anonymises the user in place and schedules a
 *      hard-delete 30 days out. Requires the current password (typed
 *      twice) so an accidental click can't destroy data. After a
 *      successful delete the API also invalidates every session, so
 *      the next page navigation kicks the user back to the login
 *      screen.
 *
 * Styled against the Living-Systems brand: cream paper, Archivo
 * headline with the italic accent, emerald CTA for export, red
 * destructive CTA for delete. See docs/design/HANDOFF.md.
 *
 * The client component is deliberately small — the heavy lifting
 * happens on the server. We do NOT pre-fetch any data on this page
 * because both actions are write-only.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { ArrowLeft, ShieldCheck } from 'lucide-react';

import { Headline } from '@/components/ui/headline';

import { PrivacyActions } from './components/PrivacyActions';

export default function PrivacyPage(): ReactElement {
  return (
    <section className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Link
          href="/settings"
          className="inline-flex items-center gap-1.5 font-sans text-xs font-medium text-fg-muted transition-colors hover:text-emerald-deep"
        >
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
          Back to settings
        </Link>
        <div className="flex flex-col gap-2">
          <span className="inline-flex items-center gap-1.5 font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            <ShieldCheck className="h-3.5 w-3.5" aria-hidden="true" />
            Privacy
          </span>
          <Headline as="h1" size="sub">
            Your data, your <em>call</em>.
          </Headline>
          <p className="max-w-[640px] font-sans text-sm text-fg-muted">
            Export every byte we hold about you, or delete your account
            entirely. Exports run asynchronously and arrive at a
            one-time download URL within a few minutes. Deletion is
            irreversible after a 30-day recovery window.
          </p>
        </div>
      </div>

      <PrivacyActions />
    </section>
  );
}
