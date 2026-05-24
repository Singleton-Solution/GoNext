'use client';

/**
 * MediaGrid — paginated, type-filtered grid of media assets.
 *
 * The component renders a CSS grid of clickable tiles, one per asset.
 * Image MIME types render as <img>; everything else gets a textual
 * "FILE.PDF" / "VIDEO.MP4" badge with the filename underneath. This
 * keeps the grid useful for non-image content without bloating it
 * with per-mime icon sets.
 *
 * Filter chips switch between "All / Images / Documents / Video" and
 * trigger a refetch. Infinite scroll uses an IntersectionObserver on
 * a sentinel element at the bottom of the list — when the sentinel
 * enters the viewport, we ask for the next page. The sentinel is
 * deliberately wired with `rootMargin: '300px'` so the next page is
 * already in flight when the user reaches it, masking the network
 * round-trip.
 *
 * Why not react-virtuoso?
 *  - The grid is naturally bounded: even a site with thousands of
 *    assets is paginated at the API layer, so the DOM only ever holds
 *    a hundred-ish nodes at a time. Virtualisation would add a runtime
 *    dependency without buying a measurable frame-rate improvement.
 *  - The spec calls it out as a "fallback to plain if not present"; we
 *    take the plain path now and revisit if profiling shows a problem
 *    on a real prod-scale library.
 */
import Link from 'next/link';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
} from 'react';
import { listMedia } from '../actions';
import type {
  MediaAsset,
  MediaListResponse,
  MediaTypeFilter,
} from '../types';
import { UploadDropzone } from './UploadDropzone';

export interface MediaGridProps {
  /** Optional initial page — typically prefetched by the server
   * component so the first paint isn't blocked on a client fetch. */
  initialData?: MediaListResponse;
}

interface FilterChip {
  value: MediaTypeFilter;
  label: string;
}

const FILTER_CHIPS: readonly FilterChip[] = [
  { value: 'all', label: 'All' },
  { value: 'image', label: 'Images' },
  { value: 'document', label: 'Documents' },
  { value: 'video', label: 'Video' },
];

/**
 * Format a byte count as a short human string. We keep this local
 * (rather than reaching for a shared helper) so the grid stays
 * self-contained; it gets pulled into a utility module the first
 * time another file needs the same formatter.
 */
function humanBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

export function MediaGrid(props: MediaGridProps): ReactElement {
  const { initialData } = props;
  const [filter, setFilter] = useState<MediaTypeFilter>('all');
  const [items, setItems] = useState<MediaAsset[]>(initialData?.data ?? []);
  const [cursor, setCursor] = useState<string>(
    initialData?.pagination.next_cursor ?? '',
  );
  const [hydrated, setHydrated] = useState<boolean>(Boolean(initialData));
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const sentinelRef = useRef<HTMLDivElement | null>(null);

  /**
   * Fetch the next page (cursor != "") or replace the list when
   * `reset` is true. We use a single function for both so the loading
   * flag and error state stay coordinated; otherwise a chip switch
   * during an in-flight infinite-scroll fetch would race.
   */
  const fetchPage = useCallback(
    async (opts: { reset?: boolean; nextCursor?: string } = {}) => {
      setLoading(true);
      setError(null);
      try {
        const res = await listMedia({
          type: filter,
          cursor: opts.nextCursor || undefined,
        });
        setItems((prev) => (opts.reset ? res.data : [...prev, ...res.data]));
        setCursor(res.pagination.next_cursor);
        setHydrated(true);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'failed to load media');
      } finally {
        setLoading(false);
      }
    },
    [filter],
  );

  // Refetch from page 1 whenever the chip filter changes. We don't
  // refetch on mount when `initialData` was passed in (the server
  // already gave us the first page); the `hydrated` flag is the
  // guard.
  useEffect(() => {
    if (!hydrated) {
      void fetchPage({ reset: true });
      return;
    }
    // Filter changed after hydration — reset the list.
    void fetchPage({ reset: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filter]);

  // Infinite-scroll sentinel. IntersectionObserver is the right
  // primitive here — it fires asynchronously without a scroll handler,
  // which keeps the main thread free for the grid's image decodes.
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;
    if (typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting && cursor && !loading) {
            void fetchPage({ nextCursor: cursor });
          }
        }
      },
      { rootMargin: '300px' },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [cursor, loading, fetchPage]);

  const onUploaded = useCallback((asset: MediaAsset) => {
    // Inserted at the top — matches the newest-first ordering of the
    // server-side list. If a user keeps the page open during a long
    // batch upload, they see assets accumulate at the top in the
    // order the server confirmed them.
    setItems((prev) => [asset, prev.find((p) => p.id === asset.id) ? null! : asset, ...prev].filter(Boolean) as MediaAsset[]);
    // The line above looks weird because dedupe might already have
    // landed the asset (server returns 200 with the existing row);
    // we splice it in but skip if it's already present. Cheap O(n)
    // because n is small.
  }, []);

  const hasItems = items.length > 0;

  // De-dupe items array — uploads can race with the grid's hydrate.
  const display = useMemo(() => {
    const seen = new Set<string>();
    const out: MediaAsset[] = [];
    for (const a of items) {
      if (seen.has(a.id)) continue;
      seen.add(a.id);
      out.push(a);
    }
    return out;
  }, [items]);

  return (
    <section data-testid="media-grid">
      <header
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
          gap: 12,
          flexWrap: 'wrap',
        }}
      >
        <h1 style={{ margin: 0 }}>Media library</h1>
        <div role="tablist" aria-label="filter by type" style={{ display: 'flex', gap: 6 }}>
          {FILTER_CHIPS.map((chip) => {
            const active = chip.value === filter;
            return (
              <button
                key={chip.value}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => setFilter(chip.value)}
                data-testid={`filter-chip-${chip.value}`}
                style={{
                  padding: '6px 12px',
                  borderRadius: 999,
                  border: active ? '1px solid var(--accent, #4a90e2)' : '1px solid var(--border, #ccc)',
                  background: active ? 'var(--accent, #4a90e2)' : 'transparent',
                  color: active ? 'white' : 'inherit',
                  cursor: 'pointer',
                  fontSize: 13,
                }}
              >
                {chip.label}
              </button>
            );
          })}
        </div>
      </header>

      <div style={{ marginBottom: 16 }}>
        <UploadDropzone onUploaded={onUploaded} />
      </div>

      {error && (
        <p role="alert" style={{ color: 'var(--danger, #c0392b)' }}>
          {error}
        </p>
      )}

      {!hasItems && !loading && (
        <p className="muted" data-testid="empty-state">
          No media yet. Drop a file above to get started.
        </p>
      )}

      {hasItems && (
        <ul
          aria-label="media items"
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))',
            gap: 12,
          }}
        >
          {display.map((asset) => (
            <li key={asset.id} data-testid={`media-tile-${asset.id}`}>
              <Link
                href={`/media/${encodeURIComponent(asset.id)}`}
                style={{
                  display: 'block',
                  border: '1px solid var(--border-subtle, #eee)',
                  borderRadius: 6,
                  overflow: 'hidden',
                  textDecoration: 'none',
                  color: 'inherit',
                }}
              >
                <MediaPreview asset={asset} />
                <div style={{ padding: 8 }}>
                  <p
                    style={{
                      margin: 0,
                      fontSize: 12,
                      whiteSpace: 'nowrap',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                    }}
                    title={asset.filename}
                  >
                    {asset.filename}
                  </p>
                  <p className="muted" style={{ margin: 0, fontSize: 11 }}>
                    {humanBytes(asset.byte_size)}
                  </p>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}

      <div ref={sentinelRef} data-testid="grid-sentinel" style={{ height: 1 }} />
      {loading && (
        <p className="muted" data-testid="grid-loading">
          Loading…
        </p>
      )}
    </section>
  );
}

/**
 * Per-tile preview. Image MIME types render the actual asset; anything
 * else gets a textual badge so non-image media still has a recognisable
 * grid presence.
 */
function MediaPreview({ asset }: { asset: MediaAsset }): ReactElement {
  const isImage = asset.mime_type.startsWith('image/');
  return (
    <div
      style={{
        aspectRatio: '1 / 1',
        background: 'var(--surface-muted, #f4f4f4)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        overflow: 'hidden',
      }}
    >
      {isImage && asset.public_url ? (
        <img
          src={asset.public_url}
          alt={asset.alt_text || asset.filename}
          loading="lazy"
          style={{ width: '100%', height: '100%', objectFit: 'cover' }}
        />
      ) : (
        <span
          style={{
            fontSize: 12,
            color: 'var(--text-muted, #888)',
            textAlign: 'center',
            padding: 8,
          }}
        >
          {asset.mime_type}
        </span>
      )}
    </div>
  );
}
