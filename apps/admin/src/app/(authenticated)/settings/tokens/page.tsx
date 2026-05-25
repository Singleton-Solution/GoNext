/**
 * /settings/tokens — list of the current user's Personal Access Tokens.
 *
 * Restyled against the Living-Systems brand: cream paper page with an
 * Archivo display headline carrying the italic-accent rule
 * ("Personal access *tokens*."), an emerald CTA, and the token list inside
 * a paper-2 card. See docs/design/HANDOFF.md.
 *
 * The list itself loads on the client (the data is per-user, the SSR pass
 * has no session context, and the page is rarely visited so the extra
 * round-trip is unobjectionable). This server component is purely chrome.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { ArrowLeft, KeyRound, Plus } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';

import { TokensList } from './components/TokensList';

export default function TokensPage(): ReactElement {
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
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div className="flex flex-col gap-2">
            <span className="inline-flex items-center gap-1.5 font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
              <KeyRound className="h-3.5 w-3.5" aria-hidden="true" />
              Security
            </span>
            <Headline as="h1" size="sub">
              Personal access <em>tokens</em>.
            </Headline>
            <p className="max-w-[640px] font-sans text-sm text-fg-muted">
              Long-lived bearer tokens for CI jobs, the CLI, and external
              scripts. Each token carries an explicit set of scopes,
              intersected with your own capabilities at every request. Revoke
              any token from this page; a revoked token can never be
              reactivated.
            </p>
          </div>
          <Button asChild variant="emerald" size="default" data-testid="tokens-new-cta">
            <Link href="/settings/tokens/new">
              <Plus className="h-4 w-4" aria-hidden="true" />
              Create token
            </Link>
          </Button>
        </div>
      </div>

      <TokensList />
    </section>
  );
}
