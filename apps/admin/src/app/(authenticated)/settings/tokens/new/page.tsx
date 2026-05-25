/**
 * /settings/tokens/new — issue a new Personal Access Token.
 *
 * Server-rendered shell wrapping the interactive `<NewTokenFlow>` client
 * component. The chrome is brand-tokenised: cream paper page with an
 * Archivo headline carrying the italic-accent rule and the form inside a
 * paper-2 card. The form / reveal islands own everything below the head.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { ArrowLeft, KeyRound } from 'lucide-react';

import { Card, CardContent } from '@/components/ui/card';
import { Headline } from '@/components/ui/headline';

import { NewTokenFlow } from './NewTokenFlow';

export default function NewTokenPage(): ReactElement {
  return (
    <section className="mx-auto flex w-full max-w-[720px] flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Link
          href="/settings/tokens"
          className="inline-flex items-center gap-1.5 font-sans text-xs font-medium text-fg-muted transition-colors hover:text-emerald-deep"
        >
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden="true" />
          Back to tokens
        </Link>
        <span className="inline-flex items-center gap-1.5 font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          <KeyRound className="h-3.5 w-3.5" aria-hidden="true" />
          Security
        </span>
        <Headline as="h1" size="sub">
          Create a <em>new</em> token.
        </Headline>
        <p className="font-sans text-sm text-fg-muted">
          Tokens are valid for the scopes you select, intersected with your
          own capabilities. Treat them as passwords. You’ll see the plaintext
          exactly once on the next screen.
        </p>
      </div>

      <Card className="border-border bg-paper-2 shadow-xs">
        <CardContent className="p-6">
          <NewTokenFlow />
        </CardContent>
      </Card>
    </section>
  );
}
