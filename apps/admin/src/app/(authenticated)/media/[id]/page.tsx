/**
 * Media asset detail — admin page.
 *
 * Server component shell that prefetches a single asset and hands it
 * off to the client-side editor for alt-text + caption editing,
 * deletion, and storage-URL display.
 */
import { notFound } from 'next/navigation';
import type { ReactElement } from 'react';
import { serverApiFetch } from '@/lib/server-api';
import { MediaDetailClient } from './MediaDetailClient';
import type { MediaAsset } from '../types';

export const dynamic = 'force-dynamic';

async function fetchAsset(id: string): Promise<MediaAsset | null> {
  try {
    const res = await serverApiFetch(
      `/api/v1/admin/media/${encodeURIComponent(id)}`,
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
