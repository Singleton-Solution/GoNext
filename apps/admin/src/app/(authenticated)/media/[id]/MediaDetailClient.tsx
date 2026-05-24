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
 */
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import {
  useCallback,
  useState,
  type FormEvent,
  type ReactElement,
} from 'react';
import { deleteMedia, updateMedia } from '../actions';
import type { MediaAsset } from '../types';
import { ApiError } from '@/lib/api-client';

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

export function MediaDetailClient(props: MediaDetailClientProps): ReactElement {
  const router = useRouter();
  const [asset, setAsset] = useState<MediaAsset>(props.initial);
  const [altText, setAltText] = useState<string>(props.initial.alt_text);
  const [caption, setCaption] = useState<string>(props.initial.caption);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<string | null>(null);

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

  const isImage = asset.mime_type.startsWith('image/');

  return (
    <section data-testid="media-detail" style={{ display: 'grid', gap: 24, maxWidth: 960 }}>
      <header>
        <Link href="/media" style={{ fontSize: 13 }}>
          {'← Back to library'}
        </Link>
        <h1 style={{ margin: '4px 0 0' }}>{asset.filename}</h1>
      </header>

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)',
          gap: 24,
          alignItems: 'start',
        }}
      >
        <div
          data-testid="media-detail-preview"
          style={{
            background: 'var(--surface-muted, #f4f4f4)',
            borderRadius: 6,
            overflow: 'hidden',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            minHeight: 240,
          }}
        >
          {isImage && asset.public_url ? (
            <img
              src={asset.public_url}
              alt={asset.alt_text || asset.filename}
              style={{ maxWidth: '100%', maxHeight: 480, display: 'block' }}
            />
          ) : (
            <div style={{ textAlign: 'center', padding: 24 }}>
              <p style={{ margin: 0, fontSize: 13 }}>{asset.mime_type}</p>
              <p className="muted" style={{ margin: '4px 0 0', fontSize: 11 }}>
                preview unavailable
              </p>
            </div>
          )}
        </div>

        <form onSubmit={onSave} style={{ display: 'grid', gap: 12 }}>
          <label style={{ display: 'grid', gap: 4 }}>
            <span style={{ fontSize: 13, fontWeight: 600 }}>Alt text</span>
            <textarea
              data-testid="alt-text-input"
              value={altText}
              onChange={(e) => setAltText(e.target.value)}
              rows={3}
              maxLength={2048}
              placeholder="Describe this image for screen readers"
              style={{ font: 'inherit', padding: 8 }}
            />
            <span className="muted" style={{ fontSize: 11 }}>
              {altText.length} / 2048
            </span>
          </label>

          <label style={{ display: 'grid', gap: 4 }}>
            <span style={{ fontSize: 13, fontWeight: 600 }}>Caption</span>
            <textarea
              data-testid="caption-input"
              value={caption}
              onChange={(e) => setCaption(e.target.value)}
              rows={3}
              maxLength={4096}
              style={{ font: 'inherit', padding: 8 }}
            />
            <span className="muted" style={{ fontSize: 11 }}>
              {caption.length} / 4096
            </span>
          </label>

          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <button
              type="submit"
              disabled={saving}
              data-testid="save-button"
              style={{ padding: '6px 12px' }}
            >
              {saving ? 'Saving…' : 'Save changes'}
            </button>
            {savedAt && !saving && (
              <span className="muted" style={{ fontSize: 12 }} data-testid="save-confirmation">
                Saved.
              </span>
            )}
          </div>

          {error && (
            <p role="alert" style={{ color: 'var(--danger, #c0392b)', margin: 0 }}>
              {error}
            </p>
          )}
        </form>
      </div>

      <dl
        data-testid="media-detail-metadata"
        style={{ display: 'grid', gridTemplateColumns: '160px 1fr', gap: '4px 12px', fontSize: 13 }}
      >
        <dt style={{ color: 'var(--text-muted, #888)' }}>Filename</dt>
        <dd style={{ margin: 0 }}>{asset.filename}</dd>

        <dt style={{ color: 'var(--text-muted, #888)' }}>Storage URL</dt>
        <dd style={{ margin: 0, wordBreak: 'break-all' }}>
          {asset.public_url ? (
            <a href={asset.public_url} target="_blank" rel="noreferrer">
              {asset.public_url}
            </a>
          ) : (
            <span className="muted">(unavailable)</span>
          )}
        </dd>

        <dt style={{ color: 'var(--text-muted, #888)' }}>MIME</dt>
        <dd style={{ margin: 0 }}>{asset.mime_type}</dd>

        <dt style={{ color: 'var(--text-muted, #888)' }}>Size</dt>
        <dd style={{ margin: 0 }}>{asset.byte_size} bytes</dd>

        {asset.width && asset.height && (
          <>
            <dt style={{ color: 'var(--text-muted, #888)' }}>Dimensions</dt>
            <dd style={{ margin: 0 }}>
              {asset.width} × {asset.height}
            </dd>
          </>
        )}

        <dt style={{ color: 'var(--text-muted, #888)' }}>Uploaded</dt>
        <dd style={{ margin: 0 }}>{asset.created_at}</dd>

        <dt style={{ color: 'var(--text-muted, #888)' }}>Updated</dt>
        <dd style={{ margin: 0 }}>{asset.updated_at}</dd>
      </dl>

      <div>
        <button
          type="button"
          onClick={onDelete}
          disabled={deleting}
          data-testid="delete-button"
          style={{
            padding: '6px 12px',
            background: 'var(--danger, #c0392b)',
            color: 'white',
            border: 'none',
            borderRadius: 4,
            cursor: deleting ? 'wait' : 'pointer',
          }}
        >
          {deleting ? 'Deleting…' : 'Delete asset'}
        </button>
      </div>
    </section>
  );
}
