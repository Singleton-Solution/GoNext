/**
 * Plugin detail — server component.
 *
 * Fetches `GET /api/v1/plugins/:name` and hands the result to
 * `<PluginDetailView>`. Like the list page, the fetch is defensive:
 * any failure renders a friendly "couldn't load" panel rather than
 * crashing.
 *
 * Route segment params are awaited per the Next.js 15 App Router
 * convention; the page is force-dynamic so a state change reflects on
 * the next navigation without manual revalidation.
 */
import { cookies } from 'next/headers';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import type { ReactElement } from 'react';
import { apiBaseUrl } from '../../api-client';
import type { PluginRecord } from '../types';
import { PluginDetailView } from './PluginDetailView';

export const dynamic = 'force-dynamic';

interface FetchOne {
  plugin: PluginRecord | null;
  notFound: boolean;
  error?: string;
}

async function fetchPlugin(name: string): Promise<FetchOne> {
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
  const url = `${apiBaseUrl.replace(/\/$/, '')}/api/v1/plugins/${encodeURIComponent(name)}`;
  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
        ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      },
      cache: 'no-store',
    });
    if (res.status === 404) {
      return { plugin: null, notFound: true };
    }
    if (!res.ok) {
      return { plugin: null, notFound: false, error: `HTTP ${res.status}` };
    }
    const data = (await res.json()) as PluginRecord;
    return { plugin: data, notFound: false };
  } catch (err) {
    const reason = err instanceof Error ? err.message : 'network error';
    return { plugin: null, notFound: false, error: reason };
  }
}

interface PluginDetailPageProps {
  params: Promise<{ name: string }>;
}

export default async function PluginDetailPage({
  params,
}: PluginDetailPageProps): Promise<ReactElement> {
  const { name } = await params;
  const { plugin, notFound: missing, error } = await fetchPlugin(name);
  if (missing) {
    notFound();
  }
  if (!plugin) {
    return (
      <section>
        <p style={{ marginBottom: 12 }}>
          <Link href="/plugins">← Back to plugins</Link>
        </p>
        <div
          role="alert"
          style={{
            padding: '10px 12px',
            border: '1px solid #fecaca',
            background: '#fef2f2',
            color: '#991b1b',
            borderRadius: 6,
            fontSize: 13,
          }}
        >
          Couldn’t load plugin &quot;{name}&quot; ({error ?? 'no data'}). The
          endpoint may not be deployed yet (issues #340 / #341).
        </div>
      </section>
    );
  }
  return (
    <>
      <p style={{ marginBottom: 12 }}>
        <Link href="/plugins">← Back to plugins</Link>
      </p>
      <PluginDetailView plugin={plugin} />
    </>
  );
}
