/**
 * Edit a redirect rule. Hydrates the form from a server-fetched row.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import type { ReactElement } from 'react';
import { apiBaseUrl } from '../../api-client';
import { RedirectForm } from '../RedirectForm';
import type { Redirect } from '../types';

export const dynamic = 'force-dynamic';

async function fetchRedirect(id: string): Promise<Redirect | null> {
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
  const url = `${apiBaseUrl}/api/v1/admin/redirects/${encodeURIComponent(id)}`;
  const res = await fetch(url, {
    method: 'GET',
    headers: {
      Accept: 'application/json',
      ...(cookieHeader ? { Cookie: cookieHeader } : {}),
    },
    cache: 'no-store',
  });
  if (!res.ok) {
    return null;
  }
  return (await res.json()) as Redirect;
}

export default async function EditRedirectPage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<ReactElement> {
  const { id } = await params;
  const initial = await fetchRedirect(id);
  if (!initial) {
    notFound();
  }
  return (
    <section>
      <p>
        <Link href="/redirects">&larr; Redirects</Link>
      </p>
      <h1>Edit redirect</h1>
      <p className="muted">
        Edits take effect on the next engine reload (instant after save).
      </p>
      <RedirectForm initial={initial} />
    </section>
  );
}
