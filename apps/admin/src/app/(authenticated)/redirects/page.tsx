/**
 * Redirects list page — server component.
 *
 * Repainted in the Living-Systems brand (PR #432). The page head uses
 * the signature italic-accent <Headline> ("301 redirect *rules*."),
 * the body sits on the cream-paper surface, and the list itself
 * delegates to <RedirectsListClient> which now wears the brand-token
 * card / tab / mono-path styling.
 */
import { Suspense, type ReactElement } from 'react';
import Link from 'next/link';
import { serverApiFetch } from '@/lib/server-api';
import { Headline } from '@/components/ui/headline';
import { Button } from '@/components/ui/button';
import { RedirectsListClient } from './RedirectsListClient';
import type { RedirectListResponse } from './types';

export const dynamic = 'force-dynamic';

async function fetchInitial(): Promise<{ data: RedirectListResponse | null; error: string | null }> {
  try {
    const res = await serverApiFetch('/api/v1/admin/redirects?limit=30');
    if (!res.ok) {
      return { data: null, error: `HTTP ${res.status}` };
    }
    const json = (await res.json()) as RedirectListResponse;
    return {
      data: {
        data: Array.isArray(json.data) ? json.data : [],
        pagination: json.pagination ?? { next_cursor: '' },
      },
      error: null,
    };
  } catch (err) {
    return { data: null, error: err instanceof Error ? err.message : 'network error' };
  }
}

/**
 * Soft skeleton — paper-3 row blocks. Tuned to roughly match the
 * height of a real row in the redirect table so the layout doesn't
 * jump when content lands.
 */
function ListSkeleton(): ReactElement {
  return (
    <div
      aria-busy="true"
      aria-live="polite"
      data-testid="redirects-skeleton"
      className="rounded-lg border border-border bg-paper-2 p-4 shadow-xs"
    >
      <span className="sr-only">Loading redirects…</span>
      {Array.from({ length: 4 }).map((_, idx) => (
        <div
          key={idx}
          className="my-2 h-8 rounded-md bg-paper-3 opacity-60"
        />
      ))}
    </div>
  );
}

function FailureState({ reason }: { reason: string }): ReactElement {
  return (
    <div
      role="alert"
      data-testid="redirects-failure"
      className="rounded-lg border border-danger/40 bg-danger-soft p-4 text-danger"
    >
      <p className="m-0 font-display font-bold">Couldn&apos;t load redirects.</p>
      <p className="mt-1 text-sm text-ink-soft">
        The list could not be fetched ({reason}). Check the API logs and
        your network access, or refresh once the API recovers.
      </p>
    </div>
  );
}

export default async function RedirectsPage(): Promise<ReactElement> {
  const { data, error } = await fetchInitial();
  return (
    <section className="flex flex-col gap-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <span className="eyebrow">Routing &middot; redirects</span>
          <Headline as="h1" size="page">
            301 redirect <em>rules</em>.
          </Headline>
          <p className="lead max-w-[58ch]">
            Send visitors from a legacy path to its current location.
            Literal rules match exact paths in O(1); regex rules support
            capture-group substitution. The middleware runs ahead of the
            renderer, so a matched rule never spends a database round-trip
            on a 404.
          </p>
        </div>
        <Button asChild variant="emerald" size="default">
          <Link href="/redirects/new">New redirect</Link>
        </Button>
      </header>

      <Suspense fallback={<ListSkeleton />}>
        {error || !data ? (
          <FailureState reason={error ?? 'no data'} />
        ) : (
          <RedirectsListClient initialData={data} />
        )}
      </Suspense>
    </section>
  );
}
