/**
 * Media asset detail — admin page.
 *
 * Server component shell that prefetches a single asset and hands it
 * off to the client-side editor for alt-text + caption editing,
 * deletion, and storage-URL display.
 */
import { cookies } from 'next/headers';
import { notFound } from 'next/navigation';
import type { ReactElement } from 'react';
import { apiBaseUrl } from '../../api-client';
import { MediaDetailClient } from './MediaDetailClient';
import type { MediaAsset } from '../types';

export const dynamic = 'force-dynamic';

async function fetchAsset(id: string): Promise<MediaAsset | null> {
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
  try {
    const res = await fetch(
      `${apiBaseUrl}/api/v1/admin/media/${encodeURIComponent(id)}`,
      {
        method: 'GET',
        headers: {
          Accept: 'application/json',
          ...(cookieHeader ? { Cookie: cookieHeader } : {}),
        },
        cache: 'no-store',
      },
    );
    if (res.status === 404) return null;
    if (!res.ok) return null;
    return (await res.json()) as MediaAsset;
  } catch {
    return null;
  }
}

export default async function MediaDetailPage(props: {
  params: Promise<{ id: string }>;
}): Promise<ReactElement> {
  const { id } = await props.params;
  const asset = await fetchAsset(id);
  if (!asset) {
    notFound();
  }
  return <MediaDetailClient initial={asset} />;
}
