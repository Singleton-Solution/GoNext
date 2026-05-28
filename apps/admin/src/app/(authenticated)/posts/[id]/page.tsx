/**
 * Post detail / edit screen (issue #35).
 *
 * Layout: 1fr / 320px split. Left column carries the title input,
 * slug input, status dropdown, and the BlockEditCanvas hosting the
 * editor. Right column is the inspector sidebar (status, schedule,
 * SEO).
 *
 * The block editor is driven by `useAutosave` from
 * `packages/ts/blocks-editor/src/autosave` — every change is debounced
 * + autosaved against /api/v1/posts/{id}/autosave. The "Save changes"
 * button on top of the page promotes the autosave content into the
 * canonical row via PATCH /api/v1/posts/{id}.
 *
 * Brand treatment ("Living systems") is preserved from the previous
 * stub; the editor lives inside a paper-2 card under the title input
 * so the surface remains coherent.
 */
'use client';

import type { ReactElement } from 'react';
import { useMemo, useState } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import {
  Calendar,
  ChevronLeft,
  Eye,
  Globe,
  Loader2,
  Save,
  Tag,
  User,
} from 'lucide-react';
import {
  BlockEditCanvas,
  defaultCoreBlocks,
  useAutosave,
} from '@gonext/blocks-editor';
import { BlockRegistry } from '@gonext/blocks-sdk';
import type { BlockTree } from '@gonext/blocks-sdk';
import type { components } from '@gonext/api-types';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api } from '@/lib/api-client';

/**
 * Status alphabet — sourced from the OpenAPI spec (issue #514 follow-up).
 * The editor UI only exposes the four user-facing values; `pending` and
 * `trash` are valid server states but flow in through the list screen
 * (bulk actions) rather than this dropdown.
 */
type PostStatus = components['schemas']['Post']['status'];

/**
 * Body for `PATCH /api/v1/posts/{id}`.
 *
 * Issue #514 follow-up: based on the spec's `PostUpdate` schema. The
 * spec types `status` as `string`; we tighten it here to the strict
 * enum so the dropdown handler can't smuggle in a bogus value. The
 * spec types `content_blocks` as an opaque `Record<string, never>`
 * placeholder; the editor needs the typed `BlockTree`, so we override
 * that field.
 */
type UpdatePostBody = Omit<
  components['schemas']['PostUpdate'],
  'status' | 'content_blocks'
> & {
  status?: PostStatus;
  content_blocks?: BlockTree;
};

export default function PostDetailPage(): ReactElement {
  const params = useParams<{ id: string }>();
  const postId = params?.id ?? 'new';

  const [title, setTitle] = useState('Untitled post');
  const [slug, setSlug] = useState('untitled-post');
  const [excerpt, setExcerpt] = useState('');
  const [status, setStatus] = useState<PostStatus>('draft');
  const [blocks, setBlocks] = useState<BlockTree>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Build the registry once per page mount. defaultCoreBlocks
  // registers paragraph + heading; subsequent blocks ship from the
  // plugin layer.
  const registry = useMemo(() => {
    const r = new BlockRegistry();
    defaultCoreBlocks(r);
    return r;
  }, []);

  // Autosave wires through to /api/v1/posts/{postId}/autosave with the
  // 30s debounced default. The hook returns a status pip we surface in
  // the toolbar so the user knows their work is safe.
  const autosave = useAutosave(postId, blocks);

  const onSave = async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const body: UpdatePostBody = {
        title,
        slug,
        status,
        content_blocks: blocks,
      };
      // The API enforces If-Match for optimistic concurrency; this
      // page is the first to PATCH, so we leave the header absent
      // and let the next iteration populate it from the load
      // response. In the meantime the autosave path is the
      // failure-resistant write — this button just promotes it.
      await api.patch<unknown>(`/api/v1/posts/${encodeURIComponent(postId)}`, body);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section data-testid="post-detail" className="flex flex-col gap-6">
      {/* Crumb + page head */}
      <div className="flex flex-col gap-3">
        <Link
          href="/posts"
          className="inline-flex w-fit items-center gap-1 text-xs font-medium text-fg-subtle hover:text-emerald-deep"
        >
          <ChevronLeft aria-hidden="true" width={13} height={13} />
          Back to posts
        </Link>
        <div className="flex flex-wrap items-end justify-between gap-6 border-b border-border pb-6">
          <div>
            <Headline as="h1" size="page" className="text-[clamp(32px,4vw,44px)]">
              Edit <em>post</em>.
            </Headline>
            <p className="mt-[10px] text-sm text-fg-muted">
              Update the metadata, slug, and publish state.{' '}
              <span className="font-mono text-xs">#{postId}</span>
            </p>
          </div>
          <div className="flex items-center gap-3">
            <AutosaveStatusPip status={autosave.status} error={autosave.error} />
            <Button variant="default" asChild>
              <Link href="/posts">Cancel</Link>
            </Button>
            <Button
              variant="emerald"
              onClick={() => void onSave()}
              disabled={saving}
              data-testid="post-save"
            >
              {saving ? (
                <Loader2 aria-hidden="true" width={14} height={14} className="animate-spin" />
              ) : (
                <Save aria-hidden="true" width={14} height={14} />
              )}
              {saving ? 'Saving…' : 'Save changes'}
            </Button>
          </div>
        </div>
        {saveError ? (
          <p
            role="alert"
            className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-900"
            data-testid="post-save-error"
          >
            {saveError}
          </p>
        ) : null}
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_320px]">
        <div className="flex flex-col gap-5">
          {/* Title + slug + excerpt + status */}
          <div className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs">
            <div className="flex flex-col gap-2">
              <Label htmlFor="post-title" className="text-fg-subtle">
                Title
              </Label>
              <input
                id="post-title"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                className="w-full bg-transparent font-display text-3xl font-bold leading-tight tracking-tight text-ink outline-none placeholder:text-fg-faint focus:outline-none"
                placeholder="What's this post about?"
              />
            </div>

            <div className="mt-5 flex flex-col gap-2">
              <Label htmlFor="post-slug" className="text-fg-subtle">
                Slug
              </Label>
              <div className="flex items-center rounded-md border border-border bg-paper transition-colors focus-within:border-emerald focus-within:shadow-focus">
                <span className="pl-3 font-mono text-xs text-fg-subtle">
                  /blog/
                </span>
                <Input
                  id="post-slug"
                  value={slug}
                  onChange={(e) => setSlug(e.target.value)}
                  className="border-0 bg-transparent font-mono focus-visible:ring-0 focus-visible:shadow-none"
                />
              </div>
            </div>

            <div className="mt-5 flex flex-col gap-2">
              <Label htmlFor="post-excerpt" className="text-fg-subtle">
                Excerpt
              </Label>
              <textarea
                id="post-excerpt"
                value={excerpt}
                onChange={(e) => setExcerpt(e.target.value)}
                rows={4}
                placeholder="A short summary used for previews and search snippets."
                className="rounded-md border border-border bg-paper p-3 font-sans text-sm text-ink outline-none transition-colors placeholder:text-fg-faint focus:border-emerald focus:shadow-focus"
              />
            </div>

            <div className="mt-5 flex flex-col gap-2">
              <Label htmlFor="status-select" className="text-fg-subtle">
                Status
              </Label>
              <select
                id="status-select"
                value={status}
                onChange={(e) => setStatus(e.target.value as PostStatus)}
                className="rounded-md border border-border bg-paper px-3 py-2 font-sans text-sm text-ink transition-colors focus:border-emerald focus:shadow-focus focus:outline-none"
                data-testid="post-status"
              >
                <option value="draft">Draft</option>
                <option value="publish">Publish</option>
                <option value="future">Scheduled</option>
                <option value="private">Private</option>
              </select>
            </div>
          </div>

          {/* Block editor canvas */}
          <div
            className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs"
            data-testid="post-editor"
          >
            <Headline as="h2" size="sub" className="text-xl">
              Block <em>editor</em>.
            </Headline>
            <p className="mt-2 text-sm text-fg-muted">
              Compose your post by adding blocks below. Changes are
              autosaved every 30 seconds; &ldquo;Save changes&rdquo; promotes the
              autosave to the canonical row.
            </p>
            <div className="mt-5 border-t border-border pt-5">
              <BlockEditCanvas
                registry={registry}
                blocks={blocks}
                context={{ postId }}
                loadingFallback={
                  <div className="flex items-center gap-2 text-sm text-fg-muted">
                    <Loader2 width={14} height={14} className="animate-spin" />
                    Loading block…
                  </div>
                }
              />
              {blocks.length === 0 ? (
                <button
                  type="button"
                  onClick={() =>
                    setBlocks([
                      {
                        type: 'paragraph',
                        attributes: { text: '' },
                      },
                    ])
                  }
                  className="mt-4 inline-flex items-center gap-1.5 rounded-md border border-dashed border-border bg-paper px-3 py-2 text-xs font-medium text-fg-muted transition-colors hover:border-emerald hover:text-emerald-deep"
                  data-testid="post-add-block"
                >
                  + Add your first block
                </button>
              ) : null}
            </div>
          </div>
        </div>

        {/* Sidebar inspector */}
        <aside
          className="flex flex-col gap-4"
          aria-label="Post metadata inspector"
        >
          <div className="rounded-lg border border-border bg-paper-2 shadow-xs">
            <div className="border-b border-border px-5 py-3">
              <h3 className="font-sans text-sm font-semibold text-ink">
                Status
              </h3>
            </div>
            <div className="flex flex-col gap-3 p-5">
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium text-fg-subtle">
                  Current
                </span>
                <Badge dot variant={status === 'publish' ? 'success' : status === 'future' ? 'lavender' : status === 'private' ? 'ink' : 'default'}>
                  {status === 'publish'
                    ? 'Published'
                    : status === 'future'
                      ? 'Scheduled'
                      : status === 'private'
                        ? 'Private'
                        : 'Draft'}
                </Badge>
              </div>
            </div>
          </div>

          <div className="rounded-lg border border-border bg-paper-2 shadow-xs">
            <div className="border-b border-border px-5 py-3">
              <h3 className="font-sans text-sm font-semibold text-ink">
                Schedule
              </h3>
            </div>
            <ul className="flex flex-col gap-3 p-5 text-sm">
              <li className="flex items-center justify-between">
                <span className="inline-flex items-center gap-2 text-xs font-medium text-fg-subtle">
                  <Calendar aria-hidden="true" width={13} height={13} />
                  Created
                </span>
                <span className="font-mono text-xs text-ink-soft">—</span>
              </li>
              <li className="flex items-center justify-between">
                <span className="inline-flex items-center gap-2 text-xs font-medium text-fg-subtle">
                  <Calendar aria-hidden="true" width={13} height={13} />
                  Updated
                </span>
                <span className="font-mono text-xs text-ink-soft">
                  {autosave.lastSavedAt
                    ? autosave.lastSavedAt.toLocaleTimeString()
                    : '—'}
                </span>
              </li>
              <li className="flex items-center justify-between">
                <span className="inline-flex items-center gap-2 text-xs font-medium text-fg-subtle">
                  <Eye aria-hidden="true" width={13} height={13} />
                  Visibility
                </span>
                <Badge variant="emerald">Public</Badge>
              </li>
              <li className="flex items-center justify-between">
                <span className="inline-flex items-center gap-2 text-xs font-medium text-fg-subtle">
                  <User aria-hidden="true" width={13} height={13} />
                  Author
                </span>
                <span className="text-xs text-ink-soft">You</span>
              </li>
            </ul>
          </div>

          <div className="rounded-lg border border-border bg-paper-2 shadow-xs">
            <div className="border-b border-border px-5 py-3">
              <h3 className="font-sans text-sm font-semibold text-ink">
                Categories &amp; tags
              </h3>
            </div>
            <div className="flex flex-col gap-3 p-5">
              <div className="flex flex-wrap gap-2">
                <Badge variant="emerald">
                  <Tag aria-hidden="true" width={10} height={10} />
                  coffee
                </Badge>
              </div>
              <Input placeholder="Add a tag…" />
            </div>
          </div>

          <div className="rounded-lg border border-border bg-paper-2 shadow-xs">
            <div className="border-b border-border px-5 py-3">
              <h3 className="font-sans text-sm font-semibold text-ink">
                <span className="inline-flex items-center gap-2">
                  <Globe aria-hidden="true" width={13} height={13} />
                  SEO
                </span>
              </h3>
            </div>
            <div className="flex flex-col gap-3 p-5">
              <div className="flex flex-col gap-2">
                <Label htmlFor="seo-title" className="text-fg-subtle">
                  Meta title
                </Label>
                <Input id="seo-title" placeholder="Title for search results" />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="seo-desc" className="text-fg-subtle">
                  Meta description
                </Label>
                <textarea
                  id="seo-desc"
                  rows={3}
                  placeholder="A short summary, around 150 characters."
                  className="rounded-md border border-border bg-paper p-3 font-sans text-sm text-ink outline-none transition-colors placeholder:text-fg-faint focus:border-emerald focus:shadow-focus"
                />
              </div>
            </div>
          </div>
        </aside>
      </div>
    </section>
  );
}

interface AutosaveStatusPipProps {
  status: 'idle' | 'saving' | 'saved' | 'error';
  error: string | null;
}

/**
 * AutosaveStatusPip — tiny inline indicator next to the Save button.
 * Mirrors the AutosaveIndicator component shipped by the blocks-
 * editor package; we render it ourselves here so the visual idiom
 * matches the rest of the page-head row.
 */
function AutosaveStatusPip({ status, error }: AutosaveStatusPipProps): ReactElement {
  if (status === 'idle') {
    return <span className="text-xs text-fg-subtle">Autosave: idle</span>;
  }
  if (status === 'saving') {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-fg-muted">
        <Loader2 width={11} height={11} className="animate-spin" />
        Autosaving…
      </span>
    );
  }
  if (status === 'saved') {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-emerald-deep">
        Autosaved
      </span>
    );
  }
  return (
    <span
      className="inline-flex items-center gap-1 text-xs text-amber-700"
      title={error ?? undefined}
    >
      Autosave failed
    </span>
  );
}
