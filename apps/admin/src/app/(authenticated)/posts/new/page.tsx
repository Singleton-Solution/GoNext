/**
 * New post — admin form.
 *
 * Thin Client Component that POSTs a minimal draft (title, slug, status,
 * empty `content_blocks`) to `/api/v1/posts` and then routes the operator
 * into the existing post editor at `/posts/{new-id}`. The editor takes
 * over for body composition; this page only exists so the "New post" CTA
 * on the list view stops 404'ing (issue #507).
 *
 * Slug derivation
 * ===============
 * Operators rarely care about the slug at creation time — they want to
 * start writing. When the slug input is blank we auto-derive it from the
 * title (lowercased, alphanumerics + hyphens, collapsed repeats). The
 * server is still authoritative; this is a convenience so the first
 * write succeeds.
 *
 * Brand: the page-head follows the moodboard pattern — display-type
 * headline with the italic-serif accent on `*next*` ("Write your *next*
 * post."), card layout for the form, emerald primary on Create.
 */
'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import {
  useState,
  type FormEvent,
  type ReactElement,
} from 'react';
import { ChevronLeft, Loader2, Plus } from 'lucide-react';
import type { components } from '@gonext/api-types';

import { ApiError, api } from '@/lib/api-client';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

/**
 * Status alphabet — sourced from the spec (issue #514 follow-up). The
 * form only exposes the four operator-facing states; `pending` and
 * `trash` (also in the spec enum) are workflow transitions that flow
 * through other surfaces (review queue, list bulk actions).
 */
type PostStatus = components['schemas']['Post']['status'];

/**
 * Body posted to `/api/v1/posts`.
 *
 * Issue #514 follow-up: derived from the spec's `PostCreate` schema.
 * Two local overrides:
 *   • `title`/`slug`/`status` are required by the form (the spec
 *     marks them optional because the server fills sensible defaults
 *     when omitted, but the UI always sends them).
 *   • `content_blocks` is typed as a `Record<string, never>` placeholder
 *     in the spec (the spec models block-tree JSON as opaque). The
 *     create form sends an empty array to seed the post; the spec will
 *     gain a typed block-tree schema later (issue #514 follow-up).
 */
type CreatePostBody = Omit<
  components['schemas']['PostCreate'],
  'title' | 'slug' | 'status' | 'content_blocks'
> & {
  title: string;
  slug: string;
  status: PostStatus;
  content_blocks: never[];
};

/**
 * Server response on success. The create endpoint returns the full
 * `Post` projection per the spec; the form only reads the `id` to
 * route to the editor, so we narrow with `Pick` to avoid coupling to
 * fields the form doesn't care about.
 */
type CreatePostResponse = Pick<components['schemas']['Post'], 'id'>;

/**
 * Derive a URL-safe slug from a free-form title. Mirrors the server's
 * own normalisation closely enough that the value won't shock-change
 * after the round-trip:
 *
 *   "Hello, World!" → "hello-world"
 *
 * Returns an empty string if the input contains no alphanumerics — the
 * server will then reject the body and surface a helpful error.
 */
export function slugify(input: string): string {
  return input
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

export default function NewPostPage(): ReactElement {
  const router = useRouter();
  const [title, setTitle] = useState('');
  const [slug, setSlug] = useState('');
  const [status, setStatus] = useState<PostStatus>('draft');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    setError(null);

    const finalTitle = title.trim();
    if (!finalTitle) {
      setError('Give the post a title — even a working one helps you find it later.');
      return;
    }

    const finalSlug = slug.trim() || slugify(finalTitle);

    setSubmitting(true);
    try {
      const body: CreatePostBody = {
        title: finalTitle,
        slug: finalSlug,
        status,
        content_blocks: [],
      };
      const created = await api.post<CreatePostResponse>('/api/v1/posts', body);
      // The editor route reads the id from the URL and pulls metadata
      // from the API on mount — no client-side cache to seed here.
      router.push(`/posts/${encodeURIComponent(created.id)}`);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(
          `Couldn't create the post (HTTP ${err.status} ${err.statusText}).`,
        );
      } else {
        setError(err instanceof Error ? err.message : "Couldn't create the post.");
      }
      setSubmitting(false);
    }
  };

  return (
    <section
      data-testid="new-post-page"
      className="mx-auto flex w-full max-w-[720px] flex-col gap-6"
    >
      <div className="flex flex-col gap-3">
        <Link
          href="/posts"
          className="inline-flex w-fit items-center gap-1 text-xs font-medium text-fg-subtle hover:text-emerald-deep"
        >
          <ChevronLeft aria-hidden="true" width={13} height={13} />
          Back to posts
        </Link>
        <div className="border-b border-border pb-6">
          <Headline as="h1" size="page" className="text-[clamp(32px,4vw,44px)]">
            Write your <em>next</em> post.
          </Headline>
          <p className="mt-[10px] max-w-[480px] text-sm text-fg-muted">
            A title is all you need to start. The body opens in the block
            editor once the draft exists.
          </p>
        </div>
      </div>

      <form
        onSubmit={(event) => {
          void onSubmit(event);
        }}
        className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs"
        noValidate
      >
        <div className="flex flex-col gap-2">
          <Label htmlFor="new-post-title" className="text-fg-subtle">
            Title <span className="text-danger">*</span>
          </Label>
          <input
            id="new-post-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            required
            autoFocus
            placeholder="What's this post about?"
            className="w-full bg-transparent font-display text-3xl font-bold leading-tight tracking-tight text-ink outline-none placeholder:text-fg-faint focus:outline-none"
          />
        </div>

        <div className="mt-5 flex flex-col gap-2">
          <Label htmlFor="new-post-slug" className="text-fg-subtle">
            Slug
          </Label>
          <div className="flex items-center rounded-md border border-border bg-paper transition-colors focus-within:border-emerald focus-within:shadow-focus">
            <span className="pl-3 font-mono text-xs text-fg-subtle">
              /blog/
            </span>
            <Input
              id="new-post-slug"
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
              placeholder={title ? slugify(title) : 'auto-generated-from-title'}
              className="border-0 bg-transparent font-mono focus-visible:ring-0 focus-visible:shadow-none"
            />
          </div>
          <p className="text-xs text-fg-faint">
            Leave blank to derive from the title.
          </p>
        </div>

        <div className="mt-5 flex flex-col gap-2">
          <Label htmlFor="new-post-status" className="text-fg-subtle">
            Status
          </Label>
          <select
            id="new-post-status"
            value={status}
            onChange={(e) => setStatus(e.target.value as PostStatus)}
            className="rounded-md border border-border bg-paper px-3 py-2 font-sans text-sm text-ink transition-colors focus:border-emerald focus:shadow-focus focus:outline-none"
            data-testid="new-post-status"
          >
            <option value="draft">Draft</option>
            <option value="publish">Publish immediately</option>
            <option value="private">Private</option>
            <option value="future">Scheduled</option>
          </select>
        </div>

        {error ? (
          <p
            role="alert"
            data-testid="new-post-error"
            className="mt-5 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-900"
          >
            {error}
          </p>
        ) : null}

        <div className="mt-6 flex items-center justify-end gap-2 border-t border-border pt-5">
          <Button variant="default" asChild>
            <Link href="/posts">Cancel</Link>
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={submitting}
            data-testid="new-post-submit"
          >
            {submitting ? (
              <Loader2 aria-hidden="true" width={14} height={14} className="animate-spin" />
            ) : (
              <Plus aria-hidden="true" width={14} height={14} />
            )}
            {submitting ? 'Creating…' : 'Create draft'}
          </Button>
        </div>
      </form>
    </section>
  );
}
