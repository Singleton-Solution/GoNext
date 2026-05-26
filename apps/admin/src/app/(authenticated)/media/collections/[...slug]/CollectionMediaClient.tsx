'use client';

/**
 * CollectionMediaClient — the client island the
 * `/media/collections/[...slug]` route hands its prefetched state
 * to. Renders a folder header (path breadcrumb + name + a soft
 * description) above the existing <MediaGrid> with the matched
 * folder pre-selected.
 *
 * Issue #69.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import type {
  MediaCollection,
  MediaListResponse,
} from '../../types';
import { MediaGrid } from '../../components/MediaGrid';
import { Headline } from '@/components/ui/headline';

export interface CollectionMediaClientProps {
  collection: MediaCollection;
  collections: MediaCollection[];
  initialMedia: MediaListResponse;
}

/**
 * Render the dotted ltree path as a clickable breadcrumb. Each
 * segment links to the deeper folder so the operator can navigate
 * up the tree without going back to the root view.
 */
function Breadcrumb({
  collection,
  all,
}: {
  collection: MediaCollection;
  all: MediaCollection[];
}): ReactElement {
  const segments = collection.path.split('.');
  // Cumulative path: ["a", "a.b", "a.b.c"] for path "a.b.c".
  const items = segments.map((_, i) => {
    const path = segments.slice(0, i + 1).join('.');
    const node = all.find((c) => c.path === path);
    const href = `/media/collections/${segments
      .slice(0, i + 1)
      .map(encodeURIComponent)
      .join('/')}`;
    return { href, label: node?.name ?? segments[i] };
  });
  return (
    <nav aria-label="folder breadcrumb" className="flex flex-wrap items-center gap-1 font-sans text-xs text-fg-muted">
      <Link href="/media" className="hover:underline">
        Media
      </Link>
      {items.map((it, i) => (
        <span key={it.href} className="inline-flex items-center gap-1">
          <span aria-hidden="true">/</span>
          {i === items.length - 1 ? (
            <span className="text-ink">{it.label}</span>
          ) : (
            <Link href={it.href} className="hover:underline">
              {it.label}
            </Link>
          )}
        </span>
      ))}
    </nav>
  );
}

export function CollectionMediaClient(
  props: CollectionMediaClientProps,
): ReactElement {
  const { collection, collections, initialMedia } = props;
  return (
    <section className="flex flex-col gap-6">
      <header className="flex flex-col gap-2">
        <Breadcrumb collection={collection} all={collections} />
        <Headline as="h1" size="sub">
          {collection.name}
        </Headline>
        <p className="font-mono text-xs text-fg-subtle m-0">{collection.path}</p>
      </header>
      <MediaGrid
        initialData={initialMedia}
        initialFolderId={collection.id}
        initialCollections={collections}
      />
    </section>
  );
}
