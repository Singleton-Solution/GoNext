/**
 * Media collection (folder) view — admin page.
 *
 * Server component shell that resolves the dynamic ltree path from
 * the URL segments, prefetches the folder's media + the full
 * collection tree, and hands them off to the existing
 * <MediaGrid> client component. The grid was already collection-
 * aware; rendering it inside this route just pre-selects the folder
 * so the operator can deep-link into a folder via URL.
 *
 * Path semantics: /media/collections/marketing/2026/q1 →
 * collections.path = "marketing.2026.q1". The Go store's GetByPath
 * resolves this exactly, so the route can survive renames without
 * embedding ids in the URL.
 *
 * Issue #69.
 */
import { notFound } from 'next/navigation';
import type { ReactElement } from 'react';
import { serverApiFetch } from '@/lib/server-api';
import { CollectionMediaClient } from './CollectionMediaClient';
import type {
  CollectionListResponse,
  MediaCollection,
  MediaListResponse,
} from '../../types';

export const dynamic = 'force-dynamic';

interface PageProps {
  // Next 15 passes an async params promise so the page can suspend
  // until the runtime resolves the dynamic segment.
  params: Promise<{ slug: string[] }>;
}

async function fetchCollections(): Promise<CollectionListResponse | null> {
  try {
    const res = await serverApiFetch('/api/v1/admin/media/collections');
    if (!res.ok) return null;
    return (await res.json()) as CollectionListResponse;
  } catch {
    return null;
  }
}

async function fetchMediaInFolder(
  collectionId: string,
): Promise<MediaListResponse | null> {
  try {
    const res = await serverApiFetch(
      `/api/v1/admin/media?limit=30&collection=${encodeURIComponent(collectionId)}`,
    );
    if (!res.ok) return null;
    return (await res.json()) as MediaListResponse;
  } catch {
    return null;
  }
}

/**
 * Find the collection whose ltree path matches the URL segments.
 * The lookup is client-side over the already-fetched flat list
 * because the admin tree's hot path is already paying the cost of
 * pulling the whole list — a second round trip to GetByPath would
 * be wasteful.
 */
function findByPath(
  list: MediaCollection[],
  segments: string[],
): MediaCollection | null {
  const target = segments.join('.');
  for (const c of list) {
    if (c.path === target) return c;
  }
  return null;
}

export default async function MediaCollectionPage(
  props: PageProps,
): Promise<ReactElement> {
  const { slug } = await props.params;

  const collections = await fetchCollections();
  if (!collections) {
    notFound();
  }

  const match = findByPath(collections.data, slug);
  if (!match) {
    notFound();
  }
  const media = await fetchMediaInFolder(match.id);
  return (
    <CollectionMediaClient
      collection={match}
      collections={collections.data}
      initialMedia={media ?? { data: [], pagination: { next_cursor: '' } }}
    />
  );
}
