'use client';

/**
 * Standalone .gntheme installer (issue #13).
 *
 * A drag-and-drop drop zone backed by the
 * POST /api/v1/admin/themes/install endpoint. On success the user
 * is forwarded to /appearance/themes so the new card lands in front
 * of them; on failure the API's error message is surfaced inline.
 *
 * The drop zone, status banner, and inline file picker are
 * intentionally the same primitives used by the umbrella themes
 * page so the two surfaces feel like one feature even though they
 * live in different routes.
 */

import { useCallback, useState, type ChangeEvent, type DragEvent, type ReactElement } from 'react';
import { useRouter } from 'next/navigation';
import { Check, Loader2, Upload } from 'lucide-react';
import { cn } from '@/lib/utils';
import { installTheme } from '../themes/api-client';

type Status =
  | { kind: 'idle' }
  | { kind: 'busy'; message: string }
  | { kind: 'success'; message: string }
  | { kind: 'error'; message: string };

export function InstallerClient(): ReactElement {
  const router = useRouter();
  const [status, setStatus] = useState<Status>({ kind: 'idle' });
  const [dragOver, setDragOver] = useState(false);

  const onUpload = useCallback(
    async (file: File) => {
      if (!/\.(gntheme|zip)$/i.test(file.name)) {
        setStatus({ kind: 'error', message: 'Pick a .gntheme or .zip file.' });
        return;
      }
      setStatus({ kind: 'busy', message: `Uploading ${file.name}…` });
      try {
        const result = await installTheme(file);
        setStatus({
          kind: 'success',
          message: `Installed “${result.title}”. Redirecting…`,
        });
        // Push to the umbrella themes page so the operator sees the
        // new card in the gallery. router.refresh() forces the
        // server component to re-fetch.
        setTimeout(() => {
          router.push('/appearance/themes');
          router.refresh();
        }, 600);
      } catch (err) {
        setStatus({
          kind: 'error',
          message: err instanceof Error ? err.message : 'Install failed.',
        });
      }
    },
    [router],
  );

  const onChange = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) void onUpload(file);
    e.target.value = '';
  };

  const onDrop = (e: DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragOver(false);
    const file = e.dataTransfer.files?.[0];
    if (file) void onUpload(file);
  };

  return (
    <div className="flex flex-col gap-3">
      <div
        data-testid="install-dropzone"
        data-drag-over={dragOver || undefined}
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        className={cn(
          'relative flex flex-col items-center justify-center gap-3 rounded-lg border-2 border-dashed bg-paper-2 px-6 py-14 text-center transition-colors',
          dragOver
            ? 'border-emerald-bright bg-emerald-soft/30'
            : 'border-border hover:border-border-strong',
        )}
      >
        <Upload aria-hidden className="h-8 w-8 text-emerald-deep" />
        <div className="font-display text-xl font-extrabold tracking-tight text-ink">
          Drop a <em className="font-serif font-normal italic text-emerald-deep">.gntheme</em> here
        </div>
        <p className="max-w-[420px] text-sm leading-normal text-fg-muted">
          The archive must contain a <code className="font-mono">theme.json</code> at the root or
          inside a single top-level directory.
        </p>
        <label className="inline-flex">
          <input
            type="file"
            accept=".gntheme,.zip,application/zip"
            onChange={onChange}
            className="sr-only"
            data-testid="install-file-input"
          />
          <span className="inline-flex cursor-pointer items-center gap-[6px] rounded-md bg-emerald px-3 py-[7px] font-display text-xs font-bold leading-none text-emerald-ink shadow-xs transition-colors hover:bg-emerald-deep hover:text-paper">
            Choose file
          </span>
        </label>
      </div>
      {status.kind !== 'idle' && (
        <div
          role={status.kind === 'error' ? 'alert' : 'status'}
          data-testid={`install-status-${status.kind}`}
          className={cn(
            'flex items-center gap-2 rounded-md border px-4 py-2 font-sans text-sm',
            status.kind === 'busy' && 'border-border bg-paper-2 text-fg-muted',
            status.kind === 'success' && 'border-emerald-bright/40 bg-emerald-soft text-emerald-deep',
            status.kind === 'error' && 'border-rose-300 bg-rose-50 text-rose-900',
          )}
        >
          {status.kind === 'busy' && <Loader2 aria-hidden className="h-4 w-4 animate-spin" />}
          {status.kind === 'success' && <Check aria-hidden className="h-4 w-4" />}
          <span>{status.message}</span>
        </div>
      )}
    </div>
  );
}
