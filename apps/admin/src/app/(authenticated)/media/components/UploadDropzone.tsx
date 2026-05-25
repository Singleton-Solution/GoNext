'use client';

/**
 * UploadDropzone — drag/drop + file-picker for media uploads.
 *
 * Responsibilities:
 *  - Render a target that accepts file drops and a click-to-pick fallback.
 *  - Track per-file upload progress (queued → uploading → done | error).
 *  - Surface errors next to the offending file rather than as a toast,
 *    so an operator queueing many files at once can see at a glance
 *    which ones failed.
 *  - Emit a callback when an upload completes so the parent grid can
 *    thread the new row into its state without a full refetch.
 *
 * Visual treatment follows the Living-Systems brand bundle in
 * docs/design/ui_kits/studio/ — paper-3 sunken surface with a dashed
 * border-strong outline, an UploadCloud glyph that swaps to
 * emerald-bright on hover, and progress bars filled with emerald for
 * uploads in flight and lavender for the brief "processing" / queued
 * window. Status text is set in Geist Mono so the percentage stays
 * fixed-width across a long upload queue.
 *
 * The dropzone is intentionally NOT virtualised — a typical batch is
 * single-digit files, and even a 50-file paste would render in well
 * under 16 ms. If a power user hits that ceiling, the right answer
 * is a "queue too long, upload finishes in the background" panel
 * rather than a windowed list of files-in-flight.
 */
import {
  useCallback,
  useRef,
  useState,
  type ChangeEvent,
  type DragEvent,
  type ReactElement,
} from 'react';
import { UploadCloud, AlertCircle, CheckCircle2 } from 'lucide-react';
import { uploadMedia } from '../actions';
import type { MediaAsset, UploadProgress } from '../types';
import { ApiError } from '@/lib/api-client';

export interface UploadDropzoneProps {
  /** Called once for each successful upload; the parent typically
   * inserts the asset at the top of its grid state. */
  onUploaded: (asset: MediaAsset) => void;
  /** Optional accept list; passed through to the file input. The
   * dropzone does not enforce client-side validation — the server is
   * the source of truth, so a hand-typed file type still goes
   * through and gets the same rejection it would via curl. */
  accept?: string;
}

/**
 * Tiny id helper — uploads need a stable key for React lists and a
 * stable handle into the local state map. crypto.randomUUID is
 * available in every browser the admin supports + in jsdom test
 * runners; we fall back to a counter only if it isn't.
 */
let idSeq = 0;
function nextId(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') {
    return globalThis.crypto.randomUUID();
  }
  idSeq += 1;
  return `local-${idSeq}`;
}

/**
 * Extract a friendly error message from an unknown thrown value.
 * ApiError instances have a `payload` that may be a problem-details
 * envelope with `detail`; everything else falls back to the message.
 */
function describeError(err: unknown): string {
  if (err instanceof ApiError) {
    const payload = err.payload;
    if (
      payload &&
      typeof payload === 'object' &&
      'detail' in payload &&
      typeof (payload as { detail: unknown }).detail === 'string'
    ) {
      return (payload as { detail: string }).detail;
    }
    return err.message;
  }
  if (err instanceof Error) return err.message;
  return 'upload failed';
}

export function UploadDropzone(props: UploadDropzoneProps): ReactElement {
  const { onUploaded, accept } = props;
  const [items, setItems] = useState<UploadProgress[]>([]);
  const [isDragging, setIsDragging] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  /**
   * Patch a single upload item in the local list. Used by every
   * progress / completion / error callback. Pure setState; the
   * functional form avoids the stale-closure trap when many uploads
   * are in flight at once.
   */
  const patchItem = useCallback((id: string, patch: Partial<UploadProgress>) => {
    setItems((prev) =>
      prev.map((it) => (it.id === id ? { ...it, ...patch } : it)),
    );
  }, []);

  const startUpload = useCallback(
    async (file: File) => {
      const id = nextId();
      const initial: UploadProgress = {
        id,
        filename: file.name,
        size: file.size,
        status: 'queued',
      };
      setItems((prev) => [...prev, initial]);

      patchItem(id, { status: 'uploading', progress: 0 });
      try {
        const asset = await uploadMedia(file, {
          onProgress: (fraction) => patchItem(id, { progress: fraction }),
        });
        patchItem(id, { status: 'done', progress: 1, asset });
        onUploaded(asset);
      } catch (err) {
        patchItem(id, { status: 'error', error: describeError(err) });
      }
    },
    [onUploaded, patchItem],
  );

  const handleFiles = useCallback(
    (files: FileList | File[] | null) => {
      if (!files) return;
      for (const f of Array.from(files)) {
        // Fire-and-forget; the local state captures per-file
        // progress so we don't need to await here.
        void startUpload(f);
      }
    },
    [startUpload],
  );

  const onDrop = useCallback(
    (e: DragEvent<HTMLDivElement>) => {
      e.preventDefault();
      setIsDragging(false);
      handleFiles(e.dataTransfer?.files ?? null);
    },
    [handleFiles],
  );

  const onPick = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      handleFiles(e.target.files);
      // Reset so picking the same file twice in a row still fires
      // the change event.
      e.target.value = '';
    },
    [handleFiles],
  );

  const onClickPick = useCallback(() => {
    inputRef.current?.click();
  }, []);

  return (
    <div className="flex flex-col gap-3">
      <div
        data-testid="upload-dropzone"
        onDragOver={(e) => {
          e.preventDefault();
          setIsDragging(true);
        }}
        onDragLeave={() => setIsDragging(false)}
        onDrop={onDrop}
        // The "group" class lets the icon swap to emerald-bright on
        // hover via the dropzone wrapper — keeps the icon styling
        // co-located with the rest of the surface.
        className={[
          'group relative rounded-lg bg-paper-3',
          'border-2 border-dashed',
          'px-6 py-7 text-center',
          'transition-colors transition-shadow duration-[160ms] ease-brand',
          'hover:border-emerald',
          isDragging
            ? 'border-emerald bg-emerald-soft shadow-focus'
            : 'border-border-strong',
        ].join(' ')}
      >
        <div className="flex flex-col items-center gap-2">
          <UploadCloud
            width={28}
            height={28}
            aria-hidden="true"
            className={[
              'transition-colors duration-[160ms] ease-brand',
              'group-hover:text-emerald-bright',
              isDragging ? 'text-emerald' : 'text-fg-subtle',
            ].join(' ')}
          />
          <p className="font-sans text-sm text-ink-soft m-0">
            Drag and drop files here, or{' '}
            <button
              type="button"
              onClick={onClickPick}
              className={[
                'inline bg-transparent border-0 p-0 m-0 cursor-pointer',
                'font-sans text-sm font-medium text-emerald-deep',
                'underline-offset-4 hover:underline',
                'focus-visible:outline-none focus-visible:shadow-focus',
              ].join(' ')}
            >
              choose a file
            </button>
            .
          </p>
          <p className="font-mono text-2xs text-fg-subtle m-0">
            Images, video, audio, and files up to 50 MB.
          </p>
        </div>
        <input
          ref={inputRef}
          type="file"
          multiple
          accept={accept}
          onChange={onPick}
          className="hidden"
          data-testid="upload-file-input"
        />
      </div>

      {items.length > 0 && (
        <ul
          aria-label="upload-progress"
          className={[
            'm-0 list-none p-0',
            'rounded-lg border border-border bg-paper-2 shadow-xs',
            'overflow-hidden',
          ].join(' ')}
        >
          {items.map((it, idx) => (
            <li
              key={it.id}
              data-testid={`upload-row-${it.id}`}
              className={[
                'px-4 py-3',
                idx > 0 ? 'border-t border-border-subtle' : '',
              ].join(' ')}
            >
              <div className="flex items-center justify-between gap-3">
                <span className="font-sans text-sm text-ink truncate">
                  {it.filename}
                </span>
                <StatusBadge item={it} />
              </div>
              {(it.status === 'uploading' || it.status === 'queued') && (
                <ProgressBar
                  status={it.status}
                  value={it.progress ?? 0}
                  testid={`upload-progress-${it.id}`}
                />
              )}
              {it.status === 'error' && it.error && (
                <p
                  role="alert"
                  className="font-sans text-xs text-danger m-0 mt-1"
                >
                  {it.error}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * Status badge — a tiny right-aligned chip that mirrors the brand's
 * .tag treatment (emerald-soft for done, lavender-soft for queued,
 * danger-soft for error, mono percentage while in flight).
 */
function StatusBadge({ item }: { item: UploadProgress }): ReactElement {
  if (item.status === 'done') {
    return (
      <span className="inline-flex items-center gap-[4px] rounded-sm bg-emerald-soft px-2 py-[2px] font-sans text-2xs font-medium text-emerald-deep">
        <CheckCircle2 width={11} height={11} aria-hidden="true" />
        Done
      </span>
    );
  }
  if (item.status === 'error') {
    return (
      <span className="inline-flex items-center gap-[4px] rounded-sm bg-danger-soft px-2 py-[2px] font-sans text-2xs font-medium text-danger">
        <AlertCircle width={11} height={11} aria-hidden="true" />
        Error
      </span>
    );
  }
  if (item.status === 'queued') {
    return (
      <span className="inline-flex items-center rounded-sm bg-lavender-soft px-2 py-[2px] font-sans text-2xs font-medium text-lavender-deep">
        Queued
      </span>
    );
  }
  // uploading
  const pct =
    item.progress !== undefined
      ? `${Math.round(item.progress * 100)}%`
      : 'uploading…';
  return (
    <span className="font-mono text-2xs text-fg-muted whitespace-nowrap">
      {pct}
    </span>
  );
}

/**
 * Branded progress bar. We keep a <progress> element underneath for
 * a11y + the existing test that targets `upload-progress-{id}` as
 * data-testid; the visual fill is drawn on top with brand-token
 * colors (emerald for active uploads, lavender for the brief queued
 * window before bytes start moving).
 */
function ProgressBar({
  status,
  value,
  testid,
}: {
  status: 'uploading' | 'queued';
  value: number;
  testid: string;
}): ReactElement {
  const pct = Math.max(0, Math.min(1, value)) * 100;
  return (
    <div className="mt-2 relative h-[6px] w-full overflow-hidden rounded-pill bg-paper-3">
      <div
        className={[
          'absolute inset-y-0 left-0 rounded-pill',
          'transition-[width] duration-[160ms] ease-brand',
          status === 'queued' ? 'bg-lavender' : 'bg-emerald',
        ].join(' ')}
        style={{ width: status === 'queued' ? '12%' : `${pct}%` }}
        aria-hidden="true"
      />
      <progress
        value={value}
        max={1}
        data-testid={testid}
        className="sr-only"
      />
    </div>
  );
}
