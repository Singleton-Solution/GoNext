/**
 * New webhook subscription — server component shell.
 *
 * The actual form lives in the client island so it can read user
 * input + call the create API. We render a minimal server-side scaffold
 * so the page works without JS for the head/h1 portion.
 *
 * Brand: Living-Systems (#432). Page head follows the calm,
 * instrument-panel feel of the other operations surfaces — the italic
 * accent lands on "subscription" (the noun being created).
 */
import Link from 'next/link';
import { ChevronLeft } from 'lucide-react';
import type { ReactElement } from 'react';
import { Headline } from '@/components/ui/headline';
import { NewSubscriptionClient } from './NewSubscriptionClient';

export const dynamic = 'force-dynamic';

export default function NewWebhookPage(): ReactElement {
  return (
    <section
      data-testid="webhook-new-page"
      className="flex flex-col gap-6"
    >
      <div className="flex items-end justify-between gap-6 border-b border-border pb-6">
        <div className="flex flex-col gap-3">
          <span className="font-sans text-2xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            Integrations · Outbound · New
          </span>
          <Headline as="h1" size="sub">
            New <em>subscription</em>.
          </Headline>
          <p className="max-w-[540px] font-sans text-sm text-fg-muted">
            Configure an HTTPS endpoint to receive signed event
            notifications. The API will return a fresh HMAC secret after
            creation — copy it before leaving the page. We do not retain
            a recoverable copy.
          </p>
        </div>
        <Link
          href="/webhooks"
          className="inline-flex shrink-0 items-center gap-1 font-sans text-sm text-fg-subtle transition-colors hover:text-ink"
        >
          <ChevronLeft className="h-[13px] w-[13px]" aria-hidden="true" />
          Back to list
        </Link>
      </div>
      <NewSubscriptionClient />
    </section>
  );
}
