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
import { uploadMedia } from '../actions';
import type { MediaAsset, UploadProgress } from '../types';
import { ApiError } from '@/app/api-client';

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
    <div
      data-testid="upload-dropzone"
      onDragOver={(e) => {
        e.preventDefault();
        setIsDragging(true);
      }}
      onDragLeave={() => setIsDragging(false)}
      onDrop={onDrop}
      style={{
        border: isDragging ? '2px solid var(--accent, #4a90e2)' : '2px dashed var(--border, #ccc)',
        background: isDragging ? 'rgba(74,144,226,0.05)' : 'transparent',
        borderRadius: 8,
        padding: 24,
        textAlign: 'center',
        transition: 'background 80ms ease, border-color 80ms ease',
      }}
    >
      <p style={{ margin: 0, fontSize: 14 }}>
        Drag and drop files here, or{' '}
        <button
          type="button"
          onClick={onClickPick}
          style={{
            background: 'none',
            border: 'none',
            padding: 0,
            color: 'var(--link, #0070f3)',
            textDecoration: 'underline',
            cursor: 'pointer',
            font: 'inherit',
          }}
        >
          choose a file
        </button>
        .
      </p>
      <input
        ref={inputRef}
        type="file"
        multiple
        accept={accept}
        onChange={onPick}
        style={{ display: 'none' }}
        data-testid="upload-file-input"
      />

      {items.length > 0 && (
        <ul
          aria-label="upload-progress"
          style={{
            listStyle: 'none',
            padding: 0,
            margin: '16px 0 0',
            textAlign: 'left',
          }}
        >
          {items.map((it) => (
            <li
              key={it.id}
              data-testid={`upload-row-${it.id}`}
              style={{
                padding: '6px 8px',
                borderBottom: '1px solid var(--border-subtle, #eee)',
              }}
            >
              <div
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  fontSize: 13,
                }}
              >
                <span>{it.filename}</span>
                <span className="muted">{statusLabel(it)}</span>
              </div>
              {it.status === 'uploading' && (
                <progress
                  value={it.progress ?? 0}
                  max={1}
                  data-testid={`upload-progress-${it.id}`}
                  style={{ width: '100%', height: 4 }}
                />
              )}
              {it.status === 'error' && it.error && (
                <p
                  role="alert"
                  style={{
                    margin: '4px 0 0',
                    fontSize: 12,
                    color: 'var(--danger, #c0392b)',
                  }}
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

function statusLabel(it: UploadProgress): string {
  switch (it.status) {
    case 'queued':
      return 'queued';
    case 'uploading':
      return it.progress !== undefined
        ? `${Math.round(it.progress * 100)}%`
        : 'uploading…';
    case 'done':
      return 'done';
    case 'error':
      return 'error';
  }
}
