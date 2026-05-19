/**
 * New webhook subscription — server component shell.
 *
 * The actual form lives in the client island so it can read user
 * input + call the create API. We render a minimal server-side scaffold
 * so the page works without JS for the head/h1 portion.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { NewSubscriptionClient } from './NewSubscriptionClient';

export const dynamic = 'force-dynamic';

export default function NewWebhookPage(): ReactElement {
  return (
    <section>
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: 16,
          marginBottom: 16,
        }}
      >
        <h1 style={{ margin: 0 }}>New webhook subscription</h1>
        <Link href="/webhooks" className="muted">
          &larr; Back to list
        </Link>
      </div>
      <p className="muted" style={{ marginTop: 0 }}>
        Configure an HTTPS endpoint to receive signed event notifications.
        The API will return a fresh HMAC secret after creation — copy
        it before leaving the page. We do not retain a recoverable copy.
      </p>
      <NewSubscriptionClient />
    </section>
  );
}
