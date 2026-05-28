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
 * Filter chips switch between "All / Images / Video / Files / Audio"
 * and trigger a refetch. Infinite scroll uses an IntersectionObserver
 * on a sentinel element at the bottom of the list — when the sentinel
 * enters the viewport, we ask for the next page. The sentinel is
 * deliberately wired with `rootMargin: '300px'` so the next page is
 * already in flight when the user reaches it, masking the network
 * round-trip.
 *
 * Visual treatment follows the Living-Systems brand bundle in
 * docs/design/ui_kits/studio/ — cream paper page background, paper-2
 * tile surface with a soft border + xs shadow, emerald-tinted active
 * filter chip in pill form, mono-typeset file size, hover overlay
 * with emerald edit and lavender delete icons (Lucide).
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
import { ImagePlus } from 'lucide-react';
import {
  Pencil,
  Trash2,
  FileText,
  Film,
  Music,
  ImageIcon,
} from 'lucide-react';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type DragEvent,
  type MouseEvent,
  type ReactElement,
} from 'react';
import { EmptyState, LoadingState } from '@/components/states';
import { deleteMedia, listMedia } from '../actions';
import type {
  MediaAsset,
  MediaListResponse,
  MediaTypeFilter,
} from '../types';
import { Headline } from '@/components/ui/headline';
import { UploadDropzone } from './UploadDropzone';
import { BulkActions } from './BulkActions';
import { ALL_NODE_ID, FolderTree, MEDIA_DRAG_MIME, ROOT_NODE_ID } from './FolderTree';

export interface MediaGridProps {
  /** Optional initial page — typically prefetched by the server
   * component so the first paint isn't blocked on a client fetch. */
  initialData?: MediaListResponse;
  /**
   * Optional initial folder selection. When set, the grid pre-selects
   * this node in the FolderTree and filters its media. Sentinel values
   * `ALL_NODE_ID` / `ROOT_NODE_ID` are accepted alongside concrete
   * UUIDs. Used by the `/media/collections/[...slug]` route to
   * deep-link into a folder. Issue #69.
   */
  initialFolderId?: string;
  /**
   * Optional initial flat list of collections — used by the
   * deep-linked folder page to avoid a second round-trip on hydration.
   */
  initialCollections?: import('../types').MediaCollection[];
}

interface FilterChip {
  value: MediaTypeFilter;
  label: string;
}

/**
 * Filter chips, in spec order. "All" sits first so the keyboard tab
 * order matches the visual: the catch-all is the leftmost chip. The
 * server's mime-class predicate covers image / video / document / audio.
 */
const FILTER_CHIPS: readonly FilterChip[] = [
  { value: 'all', label: 'All' },
  { value: 'image', label: 'Images' },
  { value: 'video', label: 'Video' },
  { value: 'document', label: 'Files' },
  { value: 'audio', label: 'Audio' },
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
  const { initialData, initialFolderId, initialCollections } = props;
  const [filter, setFilter] = useState<MediaTypeFilter>('all');
  const [items, setItems] = useState<MediaAsset[]>(initialData?.data ?? []);
  const [cursor, setCursor] = useState<string>(
    initialData?.pagination.next_cursor ?? '',
  );
  const [hydrated, setHydrated] = useState<boolean>(Boolean(initialData));
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  // Folder narrowing. ALL_NODE_ID renders every asset regardless of
  // folder; ROOT_NODE_ID renders only assets sitting at the implicit
  // root (no collection_id); any other value is a folder UUID. The
  // initial value is "All media" so the legacy grid behaviour is
  // preserved out of the box. Issue #69.
  const [selectedFolder, setSelectedFolder] = useState<string | null>(
    initialFolderId ?? ALL_NODE_ID,
  );
  // Bulk selection set. We keep the set as a plain object so React
  // can memoise the dependency on selectedIds.length cheaply.
  // Issue #71.
  const [selected, setSelected] = useState<Record<string, true>>({});
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
        const collectionParam =
          selectedFolder === ALL_NODE_ID || selectedFolder === null
            ? undefined
            : selectedFolder === ROOT_NODE_ID
              ? 'root'
              : selectedFolder;
        const res = await listMedia({
          type: filter,
          cursor: opts.nextCursor || undefined,
          collection: collectionParam,
        });
        // API may return `data: null` on an empty page (pgx nil
        // slice → JSON null round-trip). Normalize to [] so the
        // spread + length reads downstream don't throw.
        const safeData = Array.isArray(res.data) ? res.data : [];
        setItems((prev) => (opts.reset ? safeData : [...prev, ...safeData]));
        setCursor(res.pagination.next_cursor);
        setHydrated(true);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'failed to load media');
      } finally {
        setLoading(false);
      }
    },
    [filter, selectedFolder],
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
    // Filter or folder selection changed after hydration — reset the
    // list and clear any pending bulk selection (rows from a previous
    // view aren't visible any more).
    setSelected({});
    void fetchPage({ reset: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filter, selectedFolder]);

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
    // order the server confirmed them. We dedupe in the `display`
    // memo below; this keeps the optimistic insert path branch-free.
    setItems((prev) => [asset, ...prev]);
  }, []);

  /**
   * Quick-delete from the hover overlay. The detail page owns the
   * "full" delete flow with confirmation + redirect; here we only
   * cover the keyboard-free tile shortcut, which the brand spec calls
   * out as the lavender icon on hover. Single click → confirm →
   * fire-and-forget; we optimistically pull the row from the list on
   * success.
   */
  const onDeleteClick = useCallback(
    async (e: MouseEvent<HTMLButtonElement>, asset: MediaAsset) => {
      // The tile is wrapped in an <a> — without preventing the default
      // bubble the browser would navigate to the detail page before
      // the confirm dialog resolved.
      e.preventDefault();
      e.stopPropagation();
      if (pendingDelete) return;
      if (
        typeof window !== 'undefined' &&
        !window.confirm(`Delete ${asset.filename}?`)
      ) {
        return;
      }
      setPendingDelete(asset.id);
      try {
        await deleteMedia(asset.id);
        setItems((prev) => prev.filter((it) => it.id !== asset.id));
      } catch (err) {
        setError(err instanceof Error ? err.message : 'delete failed');
      } finally {
        setPendingDelete(null);
      }
    },
    [pendingDelete],
  );

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

  const selectedIds = useMemo(() => Object.keys(selected), [selected]);

  const toggleSelected = useCallback((id: string) => {
    setSelected((prev) => {
      const next = { ...prev };
      if (next[id]) delete next[id];
      else next[id] = true;
      return next;
    });
  }, []);

  // After a bulk op or a drop-to-folder, refresh the grid and clear
  // the selection. The grid uses the same fetchPage path so the
  // cursor + filter state stays consistent.
  const onBulkComplete = useCallback(() => {
    setSelected({});
    void fetchPage({ reset: true });
  }, [fetchPage]);

  const onDragTileStart = useCallback(
    (e: DragEvent<HTMLAnchorElement>, asset: MediaAsset) => {
      // Drag the current selection if the dragged tile is in it;
      // otherwise drag just the single tile. The folder tree reads
      // the JSON payload from the custom MIME on drop.
      const ids = selected[asset.id] ? Object.keys(selected) : [asset.id];
      e.dataTransfer.setData(MEDIA_DRAG_MIME, JSON.stringify(ids));
      e.dataTransfer.effectAllowed = 'move';
    },
    [selected],
  );

  return (
    <section
      data-testid="media-grid"
      className="flex flex-col lg:flex-row gap-6 items-start"
    >
      <FolderTree
        selectedId={selectedFolder}
        onSelect={setSelectedFolder}
        onMediaMoved={onBulkComplete}
        initialCollections={initialCollections}
      />
      <div className="flex-1 min-w-0 flex flex-col gap-6 w-full">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-2">
          <span
            className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep"
            // Eyebrow follows the .eyebrow rule from colors_and_type.css.
          >
            GoNext admin
          </span>
          <Headline as="h1" size="sub">
            Media <em>library</em>.
          </Headline>
          <p className="font-sans text-sm text-fg-muted">
            Every asset your sites use. Drop a file to add one.
          </p>
        </div>
        <div
          role="tablist"
          aria-label="filter by type"
          className="flex flex-wrap items-center gap-[6px]"
        >
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
                className={[
                  'inline-flex items-center gap-[6px] rounded-pill px-3 py-[6px]',
                  'font-sans text-xs font-medium leading-none',
                  'transition-colors duration-[160ms] ease-brand',
                  'focus-visible:outline-none focus-visible:shadow-focus',
                  active
                    ? 'bg-emerald-soft text-emerald-deep border border-transparent'
                    : 'bg-paper-2 text-fg-muted border border-border hover:border-border-strong hover:text-ink',
                ].join(' ')}
              >
                <ChipIcon value={chip.value} active={active} />
                {chip.label}
              </button>
            );
          })}
        </div>
      </header>

      <UploadDropzone onUploaded={onUploaded} />

      <BulkActions
        selectedIds={selectedIds}
        onClear={() => setSelected({})}
        onComplete={onBulkComplete}
      />

      {error && (
        <p
          role="alert"
          className="font-sans text-sm text-danger m-0"
        >
          {error}
        </p>
      )}

      {!hasItems && !loading && (
        // Brand state surface — see `src/components/states/README.md`.
        // We render the filter-aware copy here: if a chip is active
        // but yielded nothing, switch to the search variant so the
        // mood (and the icon tile) reflects "your filter narrowed
        // nothing" instead of "first run, go for it".
        <EmptyState
          variant={filter === 'all' ? 'default' : 'search'}
          icon={ImagePlus}
          title={
            filter === 'all' ? (
              <>
                No media <em>yet</em>.
              </>
            ) : (
              <>
                Nothing in <em>this filter</em>.
              </>
            )
          }
          body={
            filter === 'all'
              ? 'Drop a file above to start your library. Images, documents, and video all live here.'
              : `Try switching back to All, or upload a new ${filter} above.`
          }
        />
      )}

      {loading && !hasItems && (
        // Inline spinner — the grid only shows the heavy SkeletonCard
        // on a top-level Suspense boundary; an in-grid refresh after
        // a chip click is a smaller moment.
        <LoadingState label="Reading the library…" />
      )}

      {hasItems && (
        <ul
          aria-label="media items"
          className="m-0 list-none p-0 grid gap-3"
          style={{
            gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))',
          }}
        >
          {display.map((asset) => (
            <li key={asset.id} data-testid={`media-tile-${asset.id}`}>
              <MediaTile
                asset={asset}
                onDelete={(e) => onDeleteClick(e, asset)}
                deleting={pendingDelete === asset.id}
                selected={Boolean(selected[asset.id])}
                onToggleSelect={() => toggleSelected(asset.id)}
                onDragStart={(e) => onDragTileStart(e, asset)}
              />
            </li>
          ))}
        </ul>
      )}

      <div
        ref={sentinelRef}
        data-testid="grid-sentinel"
        className="h-px"
        aria-hidden="true"
      />
      {loading && hasItems && (
        // Pagination spinner — small inline label, not a full
        // SkeletonCard, because the user already sees rendered tiles
        // above and we don't want to push them off-screen.
        <LoadingState label="Loading more…" data-testid="grid-loading" />
      )}
      </div>
    </section>
  );
}

/**
 * A single grid tile. Composed as an <a> so a plain click navigates
 * to the detail editor; the hover overlay's emerald-edit and
 * lavender-delete icons are absolutely positioned over the preview
 * and steal pointer events so their handlers don't bubble back into
 * the anchor.
 */
function MediaTile(props: {
  asset: MediaAsset;
  onDelete: (e: MouseEvent<HTMLButtonElement>) => void;
  deleting: boolean;
  selected: boolean;
  onToggleSelect: () => void;
  onDragStart: (e: DragEvent<HTMLAnchorElement>) => void;
}): ReactElement {
  const { asset, onDelete, deleting, selected, onToggleSelect, onDragStart } = props;
  const onCheckboxClick = useCallback(
    (e: MouseEvent<HTMLLabelElement | HTMLInputElement>) => {
      // The checkbox sits inside the anchor — stop the navigation
      // intent so a tick doesn't take us to the detail editor.
      e.preventDefault();
      e.stopPropagation();
    },
    [],
  );
  const onCheckboxChange = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      e.stopPropagation();
      onToggleSelect();
    },
    [onToggleSelect],
  );
  return (
    <Link
      href={`/media/${encodeURIComponent(asset.id)}`}
      draggable
      onDragStart={onDragStart}
      className={[
        'group block bg-paper-2 border rounded-lg overflow-hidden',
        'shadow-xs no-underline text-ink',
        'transition-all duration-[160ms] ease-brand',
        'hover:shadow-md hover:-translate-y-[2px] hover:border-border-strong',
        'focus-visible:outline-none focus-visible:shadow-focus',
        selected ? 'border-emerald shadow-md' : 'border-border',
      ].join(' ')}
    >
      <div className="relative">
        <MediaPreview asset={asset} />

        {/* Selection checkbox — anchored top-left so the hover */}
        {/* overlay's edit/delete icons (top-right) don't overlap. */}
        <label
          onClick={onCheckboxClick}
          data-testid={`tile-select-${asset.id}`}
          className={[
            'absolute left-2 top-2 inline-flex h-6 w-6 cursor-pointer items-center justify-center rounded-sm border bg-paper',
            selected
              ? 'border-emerald-deep bg-emerald-soft opacity-100'
              : 'border-border opacity-0 group-hover:opacity-100 focus-within:opacity-100',
            'transition-opacity duration-[160ms] ease-brand',
          ].join(' ')}
        >
          <input
            type="checkbox"
            checked={selected}
            onChange={onCheckboxChange}
            onClick={onCheckboxClick}
            aria-label={`Select ${asset.filename}`}
            className="h-3 w-3 cursor-pointer"
          />
        </label>

        {/* Hover overlay: emerald edit + lavender delete, per spec. */}
        <div
          className={[
            'absolute inset-0 flex items-start justify-end gap-2 p-2',
            'bg-gradient-to-b from-forest/40 via-transparent to-transparent',
            'opacity-0 group-hover:opacity-100 group-focus-within:opacity-100',
            'transition-opacity duration-[160ms] ease-brand',
            'pointer-events-none',
          ].join(' ')}
          aria-hidden="true"
        >
          <span
            className={[
              'inline-flex h-7 w-7 items-center justify-center',
              'bg-emerald text-emerald-ink rounded-sm shadow-xs',
              'pointer-events-auto',
            ].join(' ')}
            title="Edit"
            data-testid={`tile-edit-${asset.id}`}
          >
            <Pencil width={14} height={14} aria-hidden="true" />
          </span>
          <button
            type="button"
            onClick={onDelete}
            disabled={deleting}
            aria-label={`Delete ${asset.filename}`}
            data-testid={`tile-delete-${asset.id}`}
            className={[
              'inline-flex h-7 w-7 items-center justify-center',
              'bg-lavender text-lavender-soft rounded-sm shadow-xs',
              'pointer-events-auto cursor-pointer border-0',
              'transition-colors duration-[160ms] ease-brand',
              'hover:bg-lavender-deep focus-visible:outline-none focus-visible:shadow-focus',
              'disabled:opacity-50 disabled:cursor-wait',
            ].join(' ')}
          >
            <Trash2 width={14} height={14} aria-hidden="true" />
          </button>
        </div>
      </div>
      <div className="p-3 flex flex-col gap-[2px]">
        <p
          className="font-sans text-sm font-medium text-ink m-0 truncate"
          title={asset.filename}
        >
          {asset.filename}
        </p>
        <p className="font-mono text-2xs text-fg-subtle m-0">
          {humanBytes(asset.byte_size)}
        </p>
      </div>
    </Link>
  );
}

/**
 * Per-tile preview. Image MIME types render the actual asset; anything
 * else gets a Lucide icon set on a paper-3 sunken surface so non-image
 * media still has a recognisable grid presence. The mime-class fan-out
 * mirrors what the chip filter exposes — images / video / audio / files.
 */
function MediaPreview({ asset }: { asset: MediaAsset }): ReactElement {
  const isImage = asset.mime_type.startsWith('image/');
  return (
    <div
      className={[
        'aspect-square w-full bg-paper-3',
        'flex items-center justify-center overflow-hidden',
      ].join(' ')}
    >
      {isImage && asset.public_url ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={asset.public_url}
          alt={asset.alt_text || asset.filename}
          loading="lazy"
          className="w-full h-full object-cover"
        />
      ) : (
        <NonImageGlyph mime={asset.mime_type} />
      )}
    </div>
  );
}

function NonImageGlyph({ mime }: { mime: string }): ReactElement {
  const Icon = mime.startsWith('video/')
    ? Film
    : mime.startsWith('audio/')
    ? Music
    : FileText;
  return (
    <div className="flex flex-col items-center gap-2 text-fg-subtle">
      <Icon width={28} height={28} aria-hidden="true" />
      <span className="font-mono text-2xs uppercase tracking-wide">
        {shortenMime(mime)}
      </span>
    </div>
  );
}

function shortenMime(mime: string): string {
  const slash = mime.indexOf('/');
  if (slash === -1) return mime;
  return mime.slice(slash + 1).slice(0, 6);
}

/**
 * Filter-chip leading icon. Kept inline so the chip row remains a
 * single self-contained block — the icon set is small enough that a
 * shared lookup table would be more ceremony than value.
 */
function ChipIcon({
  value,
  active,
}: {
  value: MediaTypeFilter;
  active: boolean;
}): ReactElement | null {
  if (value === 'all') return null;
  const Icon =
    value === 'image'
      ? ImageIcon
      : value === 'video'
      ? Film
      : value === 'audio'
      ? Music
      : FileText;
  return (
    <Icon
      width={12}
      height={12}
      aria-hidden="true"
      className={active ? 'text-emerald-deep' : 'text-fg-subtle'}
    />
  );
}
