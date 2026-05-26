/**
 * /settings/account — account-level settings surface (issue #159).
 *
 * Today this page hosts the passkey-management list. Future
 * sub-issues will add password reset, MFA settings, and the email-
 * change flow alongside.
 */
import type { ReactElement } from 'react';
import Link from 'next/link';
import { ArrowLeft, ShieldCheck } from 'lucide-react';

import { Headline } from '@/components/ui/headline';

import { PasskeyList } from './components/PasskeyList';

export default function AccountSettingsPage(): ReactElement {
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
            Security
          </span>
          <Headline as="h1" size="sub">
            Your <em>account</em>.
          </Headline>
          <p className="max-w-[640px] font-sans text-sm text-fg-muted">
            Manage how you sign in. Passkeys are the recommended way —
            they replace passwords with a hardware-bound key the site
            never sees.
          </p>
        </div>
      </div>

      <PasskeyList />
    </section>
  );
}
