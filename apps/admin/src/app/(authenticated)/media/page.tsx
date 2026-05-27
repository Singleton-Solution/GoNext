/**
 * Media library — admin page.
 *
 * Server component shell that prefetches the first page of media and
 * hands it off to the client-side <MediaGrid>. Pattern mirrors the
 * jobs/dlq page (fetch-on-server, hydrate-on-client) so the first
 * paint isn't blocked on a client-side fetch and the operator's
 * session cookie is forwarded with the prefetch.
 *
 * Issue: media library.
 */
import type { ReactElement } from 'react';
import { serverApiFetch } from '@/lib/server-api';
import { MediaGrid } from './components/MediaGrid';
import type { MediaListResponse } from './types';

export const dynamic = 'force-dynamic';

/**
 * Fetch the first page from the API server. Returns null on failure
 * so the client island can render its own empty/error state — the UX
 * is more forgiving than a full-page crash on a transient network
 * blip.
 */
async function fetchInitial(): Promise<MediaListResponse | null> {
  try {
    const res = await serverApiFetch('/api/v1/admin/media?limit=30');
    if (!res.ok) return null;
    return (await res.json()) as MediaListResponse;
  } catch {
    return null;
  }
}

export default async function MediaPage(): Promise<ReactElement> {
  const initial = await fetchInitial();
  return (
    <MediaGrid initialData={initial ?? { data: [], pagination: { next_cursor: '' } }} />
  );
}
