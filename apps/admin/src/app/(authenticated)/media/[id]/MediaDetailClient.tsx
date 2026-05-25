'use client';

/**
 * Media detail editor — client island.
 *
 * Renders a single asset's preview alongside an inline form for
 * alt-text + caption. Both fields default to the current row's values;
 * saving issues a PATCH and updates local state with the server's
 * response (so the returned `updated_at` lands and any server-side
 * canonicalisation surfaces immediately).
 *
 * Deletion is a single button gated by a native confirm dialog — the
 * dialog is the cheapest viable speed bump and keeps the click count
 * for an intentional delete at one. On confirm we redirect back to
 * the grid; on cancel we stay put.
 *
 * The storage URL panel reveals the `public_url` field with a copy
 * button so the operator can paste it into a post body without
 * round-tripping through the media inserter.
 *
 * Visual treatment follows the Living-Systems brand bundle in
 * docs/design/ui_kits/studio/ — paper-2 preview surface on the left,
 * a metadata form on the right with the brand's Input / Button
 * primitives, EXIF / size / dimension table in Geist Mono, and the
 * storage URL rendered as a copyable mono pill.
 */
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import {
  useCallback,
  useState,
  type FormEvent,
  type ReactElement,
} from 'react';
import {
  ArrowLeft,
  Check,
  Copy,
  ExternalLink,
  Trash2,
  FileText,
  Film,
  Music,
} from 'lucide-react';
import { deleteMedia, updateMedia } from '../actions';
import type { MediaAsset } from '../types';
import { ApiError } from '@/lib/api-client';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

export interface MediaDetailClientProps {
  initial: MediaAsset;
}

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
  return 'request failed';
}

function humanBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

export function MediaDetailClient(props: MediaDetailClientProps): ReactElement {
  const router = useRouter();
  const [asset, setAsset] = useState<MediaAsset>(props.initial);
  const [altText, setAltText] = useState<string>(props.initial.alt_text);
  const [caption, setCaption] = useState<string>(props.initial.caption);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<string | null>(null);
  const [copied, setCopied] = useState<boolean>(false);

  const onSave = useCallback(
    async (e: FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      setSaving(true);
      setError(null);
      try {
        const updated = await updateMedia(asset.id, {
          alt_text: altText,
          caption,
        });
        setAsset(updated);
        setSavedAt(new Date().toISOString());
      } catch (err) {
        setError(describeError(err));
      } finally {
        setSaving(false);
      }
    },
    [altText, caption, asset.id],
  );

  const onDelete = useCallback(async () => {
    if (
      typeof window !== 'undefined' &&
      !window.confirm(`Delete ${asset.filename}? This can be undone from the trash.`)
    ) {
      return;
    }
    setDeleting(true);
    setError(null);
    try {
      await deleteMedia(asset.id);
      router.push('/media');
    } catch (err) {
      setError(describeError(err));
      setDeleting(false);
    }
  }, [asset.id, asset.filename, router]);

  const onCopyUrl = useCallback(async () => {
    if (!asset.public_url) return;
    try {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(asset.public_url);
      }
      setCopied(true);
      // Reset the confirm flash after the brand's slow tick so the
      // operator can register the change without a long-lived state.
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard can fail in headless / unfocused contexts. We deliberately
      // swallow — the link is still readable, and surfacing a banner for
      // a copy failure is more noise than signal.
    }
  }, [asset.public_url]);

  const isImage = asset.mime_type.startsWith('image/');

  return (
    <section
      data-testid="media-detail"
      className="flex flex-col gap-7 max-w-[1080px]"
    >
      <header className="flex flex-col gap-3">
        <Link
          href="/media"
          className={[
            'inline-flex items-center gap-1 self-start',
            'font-sans text-xs font-medium text-fg-muted no-underline',
            'transition-colors duration-[160ms] ease-brand',
            'hover:text-ink',
            'focus-visible:outline-none focus-visible:shadow-focus rounded-sm',
          ].join(' ')}
        >
          <ArrowLeft width={12} height={12} aria-hidden="true" />
          Back to library
        </Link>
        <div className="flex flex-col gap-2">
          <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            Media asset
          </span>
          <Headline as="h1" size="sub">
            {asset.filename}
          </Headline>
        </div>
      </header>

      <div className="grid gap-6 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] items-start">
        <div
          data-testid="media-detail-preview"
          className={[
            'bg-paper-2 border border-border rounded-lg shadow-xs overflow-hidden',
            'flex items-center justify-center',
            'min-h-[300px]',
          ].join(' ')}
        >
          {isImage && asset.public_url ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={asset.public_url}
              alt={asset.alt_text || asset.filename}
              className="block max-w-full max-h-[520px] object-contain"
            />
          ) : (
            <NonImagePreview mime={asset.mime_type} />
          )}
        </div>

        <form onSubmit={onSave} className="flex flex-col gap-4">
          <div className="flex flex-col gap-[6px]">
            <Label htmlFor="alt-text">Alt text</Label>
            <textarea
              id="alt-text"
              data-testid="alt-text-input"
              value={altText}
              onChange={(e) => setAltText(e.target.value)}
              rows={3}
              maxLength={2048}
              placeholder="Describe this image for screen readers"
              className={[
                'block w-full rounded-md border border-border bg-paper-3 px-3 py-2',
                'font-sans text-sm text-ink resize-y',
                'placeholder:text-fg-faint',
                'transition-colors transition-shadow duration-[160ms] ease-brand',
                'hover:border-border-strong',
                'focus-visible:outline-none focus-visible:border-emerald focus-visible:shadow-focus',
              ].join(' ')}
            />
            <span className="font-mono text-2xs text-fg-subtle">
              {altText.length} / 2048
            </span>
          </div>

          <div className="flex flex-col gap-[6px]">
            <Label htmlFor="caption">Caption</Label>
            <textarea
              id="caption"
              data-testid="caption-input"
              value={caption}
              onChange={(e) => setCaption(e.target.value)}
              rows={3}
              maxLength={4096}
              className={[
                'block w-full rounded-md border border-border bg-paper-3 px-3 py-2',
                'font-sans text-sm text-ink resize-y',
                'placeholder:text-fg-faint',
                'transition-colors transition-shadow duration-[160ms] ease-brand',
                'hover:border-border-strong',
                'focus-visible:outline-none focus-visible:border-emerald focus-visible:shadow-focus',
              ].join(' ')}
            />
            <span className="font-mono text-2xs text-fg-subtle">
              {caption.length} / 4096
            </span>
          </div>

          <div className="flex items-center gap-3">
            <Button
              type="submit"
              variant="emerald"
              disabled={saving}
              data-testid="save-button"
            >
              {saving ? 'Saving…' : 'Save changes'}
            </Button>
            {savedAt && !saving && (
              <span
                className="inline-flex items-center gap-1 font-sans text-xs text-emerald-deep"
                data-testid="save-confirmation"
              >
                <Check width={12} height={12} aria-hidden="true" />
                Saved.
              </span>
            )}
          </div>

          {error && (
            <p
              role="alert"
              className="font-sans text-sm text-danger m-0"
            >
              {error}
            </p>
          )}
        </form>
      </div>

      <StorageUrlPanel
        url={asset.public_url}
        copied={copied}
        onCopy={onCopyUrl}
      />

      <MetadataTable asset={asset} />

      <div className="flex justify-end pt-2 border-t border-border-subtle">
        <Button
          type="button"
          variant="destructive"
          onClick={onDelete}
          disabled={deleting}
          data-testid="delete-button"
        >
          <Trash2 width={14} height={14} aria-hidden="true" />
          {deleting ? 'Deleting…' : 'Delete asset'}
        </Button>
      </div>
    </section>
  );
}

/**
 * Storage URL panel — the operator's primary "give me the link"
 * surface. Shows the URL in Geist Mono inside a paper-3 sunken pill
 * with an emerald copy button + an external-link affordance. The
 * "Copied." confirmation lands inline and clears itself after the
 * brand's slow-tick duration.
 */
function StorageUrlPanel({
  url,
  copied,
  onCopy,
}: {
  url?: string;
  copied: boolean;
  onCopy: () => void;
}): ReactElement {
  return (
    <div
      data-testid="media-detail-storage"
      className="flex flex-col gap-2"
    >
      <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-fg-subtle">
        Storage URL
      </span>
      <div
        className={[
          'flex items-center gap-2 rounded-md border border-border bg-paper-3 pl-3 pr-1 py-[6px]',
          'transition-colors duration-[160ms] ease-brand',
          'hover:border-border-strong',
        ].join(' ')}
      >
        {url ? (
          <>
            <a
              href={url}
              target="_blank"
              rel="noreferrer"
              className={[
                'flex-1 min-w-0 font-mono text-xs text-ink-soft truncate no-underline',
                'hover:text-ink',
              ].join(' ')}
              title={url}
            >
              {url}
            </a>
            <a
              href={url}
              target="_blank"
              rel="noreferrer"
              aria-label="Open in new tab"
              className={[
                'inline-flex h-7 w-7 items-center justify-center rounded-sm text-fg-subtle',
                'transition-colors duration-[160ms] ease-brand',
                'hover:bg-paper-2 hover:text-ink',
                'focus-visible:outline-none focus-visible:shadow-focus',
              ].join(' ')}
            >
              <ExternalLink width={12} height={12} aria-hidden="true" />
            </a>
            <button
              type="button"
              onClick={onCopy}
              data-testid="copy-url-button"
              aria-label="Copy storage URL"
              className={[
                'inline-flex h-7 items-center gap-1 rounded-sm px-2',
                'font-sans text-2xs font-medium',
                'transition-colors duration-[160ms] ease-brand',
                'focus-visible:outline-none focus-visible:shadow-focus',
                copied
                  ? 'bg-emerald-soft text-emerald-deep'
                  : 'bg-emerald text-emerald-ink hover:bg-emerald-deep hover:text-paper',
              ].join(' ')}
            >
              {copied ? (
                <Check width={12} height={12} aria-hidden="true" />
              ) : (
                <Copy width={12} height={12} aria-hidden="true" />
              )}
              {copied ? 'Copied' : 'Copy'}
            </button>
          </>
        ) : (
          <span className="font-mono text-xs text-fg-faint">
            (unavailable)
          </span>
        )}
      </div>
    </div>
  );
}

/**
 * Metadata + EXIF table. The Go-side asset row carries filename, MIME,
 * byte size, dimensions, and timestamps; we render them in a mono
 * table on a paper-2 surface so the operator can scan rows quickly.
 * Width/height row only appears for image assets — for everything else
 * those fields are NULL on the wire and the row would be misleading.
 */
function MetadataTable({ asset }: { asset: MediaAsset }): ReactElement {
  const rows: Array<{ label: string; value: string }> = [
    { label: 'Filename', value: asset.filename },
    { label: 'MIME', value: asset.mime_type },
    { label: 'Size', value: humanBytes(asset.byte_size) },
  ];
  if (asset.width && asset.height) {
    rows.push({
      label: 'Dimensions',
      value: `${asset.width} × ${asset.height}`,
    });
  }
  rows.push({ label: 'Uploaded', value: asset.created_at });
  rows.push({ label: 'Updated', value: asset.updated_at });
  rows.push({ label: 'Storage key', value: asset.storage_key });

  return (
    <div
      data-testid="media-detail-metadata"
      className="rounded-lg border border-border bg-paper-2 shadow-xs overflow-hidden"
    >
      <div className="px-4 py-3 border-b border-border-subtle">
        <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-fg-subtle">
          EXIF & metadata
        </span>
      </div>
      <dl className="m-0 divide-y divide-border-subtle">
        {rows.map((r) => (
          <div
            key={r.label}
            className="grid grid-cols-[160px_minmax(0,1fr)] gap-4 px-4 py-[10px] items-baseline"
          >
            <dt className="font-sans text-xs text-fg-subtle m-0">{r.label}</dt>
            <dd className="font-mono text-xs text-ink m-0 break-all">
              {r.value}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

/**
 * Non-image preview — same Lucide glyph set as the grid, scaled up
 * for the detail surface. Falls back to a generic FileText for any
 * MIME we don't recognise so we never render a blank panel.
 */
function NonImagePreview({ mime }: { mime: string }): ReactElement {
  const Icon = mime.startsWith('video/')
    ? Film
    : mime.startsWith('audio/')
    ? Music
    : FileText;
  return (
    <div className="flex flex-col items-center gap-3 p-9 text-fg-subtle">
      <Icon width={48} height={48} aria-hidden="true" />
      <span className="font-mono text-xs">{mime}</span>
      <span className="font-sans text-xs text-fg-faint">
        Preview unavailable
      </span>
    </div>
  );
}
