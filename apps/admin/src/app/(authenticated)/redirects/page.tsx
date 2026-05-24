/**
 * Redirects list page — server component.
 *
 * Fetches the first page of rules and hands it to the client island.
 * Same shape as /webhooks/page.tsx: cookies are forwarded so the API
 * sees the admin's session, and on HTTP failure we render a friendly
 * placeholder rather than crashing.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { Suspense, type ReactElement } from 'react';
import { apiBaseUrl } from '@/lib/api-client';
import { RedirectsListClient } from './RedirectsListClient';
import type { RedirectListResponse } from './types';

export const dynamic = 'force-dynamic';

async function fetchInitial(): Promise<{ data: RedirectListResponse | null; error: string | null }> {
  let cookieHeader = '';
  try {
    const store = await cookies();
    cookieHeader = store
      .getAll()
      .map((c) => `${c.name}=${c.value}`)
      .join('; ');
  } catch {
    cookieHeader = '';
  }
  const url = `${apiBaseUrl}/api/v1/admin/redirects?limit=30`;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
        ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      },
      cache: 'no-store',
    });
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

function ListSkeleton(): ReactElement {
  return (
    <div aria-busy="true" aria-live="polite" style={{ padding: 16 }}>
      <span className="visually-hidden">Loading redirects…</span>
      {Array.from({ length: 4 }).map((_, idx) => (
        <div
          key={idx}
          style={{
            height: 32,
            margin: '8px 0',
            background: 'var(--color-border)',
            opacity: 0.4,
            borderRadius: 'var(--radius)',
          }}
        />
      ))}
    </div>
  );
}

function FailureState({ reason }: { reason: string }): ReactElement {
  return (
    <div role="alert" style={{ padding: 16 }}>
      <p>Failed to load redirects: {reason}</p>
      <p className="muted">
        The list could not be fetched. Check the API logs and your network access,
        or refresh once the API recovers.
      </p>
    </div>
  );
}

export default async function RedirectsPage(): Promise<ReactElement> {
  const { data, error } = await fetchInitial();
  return (
    <section>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 16, marginBottom: 16 }}>
        <h1 style={{ margin: 0 }}>Redirects</h1>
        <Link href="/redirects/new" className="primary-action">
          New redirect
        </Link>
      </div>
      <p className="muted" style={{ marginTop: 0 }}>
        Send visitors from a legacy path to its current location. Literal rules
        match exact paths in O(1); regex rules support capture-group
        substitution. The middleware runs ahead of the renderer, so a matched
        rule never spends a database round-trip on a 404.
      </p>
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
