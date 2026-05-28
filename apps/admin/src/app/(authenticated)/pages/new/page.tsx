/**
 * New page — admin form.
 *
 * Sister of `/posts/new`. Pages share the post-type infrastructure
 * (docs/05-admin-api.md §3.1), so we POST to `/api/v1/posts` with
 * `post_type: 'page'` to land in the right bucket. Once the row exists
 * the operator is routed to `/pages/{id}` where the existing metadata
 * editor takes over.
 *
 * The slug field is mandatory for pages because the URL is the page —
 * a draft post can ship without a finalised slug, but a page can't be
 * linked into a navigation menu until its URL is fixed. We still
 * auto-derive from the title if the field is blank, but the help text
 * makes the URL-as-identity expectation clear.
 *
 * Brand: "Create a new *page*." — italic accent on the noun, matches
 * the moodboard pattern from the pages list and the `/posts/new`
 * sibling. Card form on paper-2.
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

import { ApiError, api } from '@/lib/api-client';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

type PageStatus = 'draft' | 'publish' | 'private';

interface CreatePageBody {
  title: string;
  slug: string;
  status: PageStatus;
  post_type: 'page';
  content_blocks: never[];
}

interface CreatePageResponse {
  id: string;
}

/**
 * Pages live at flat URLs (`/about`, `/contact`) so we normalise the
 * slug into the leading-slash form callers expect. The server will
 * normalise again, but doing it here keeps the placeholder honest.
 */
export function slugifyPage(input: string): string {
  const base = input
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return base ? `/${base}` : '';
}

export default function NewPagePage(): ReactElement {
  const router = useRouter();
  const [title, setTitle] = useState('');
  const [slug, setSlug] = useState('');
  const [status, setStatus] = useState<PageStatus>('draft');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    setError(null);

    const finalTitle = title.trim();
    if (!finalTitle) {
      setError('Give the page a title — it doubles as the default <h1>.');
      return;
    }

    const finalSlug = slug.trim() || slugifyPage(finalTitle);

    setSubmitting(true);
    try {
      const body: CreatePageBody = {
        title: finalTitle,
        slug: finalSlug,
        status,
        post_type: 'page',
        content_blocks: [],
      };
      const created = await api.post<CreatePageResponse>('/api/v1/posts', body);
      router.push(`/pages/${encodeURIComponent(created.id)}`);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(
          `Couldn't create the page (HTTP ${err.status} ${err.statusText}).`,
        );
      } else {
        setError(err instanceof Error ? err.message : "Couldn't create the page.");
      }
      setSubmitting(false);
    }
  };

  return (
    <section
      data-testid="new-page-page"
      className="mx-auto flex w-full max-w-[720px] flex-col gap-6"
    >
      <div className="flex flex-col gap-3">
        <Link
          href="/pages"
          className="inline-flex w-fit items-center gap-1 text-xs font-medium text-fg-subtle hover:text-emerald-deep"
        >
          <ChevronLeft aria-hidden="true" width={13} height={13} />
          Back to pages
        </Link>
        <div className="border-b border-border pb-6">
          <Headline as="h1" size="page" className="text-[clamp(32px,4vw,44px)]">
            Create a new <em>page</em>.
          </Headline>
          <p className="mt-[10px] max-w-[480px] text-sm text-fg-muted">
            Evergreen content — about, contact, policy. Pick a title and URL,
            then open the page to add metadata and content.
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
          <Label htmlFor="new-page-title" className="text-fg-subtle">
            Title <span className="text-danger">*</span>
          </Label>
          <input
            id="new-page-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            required
            autoFocus
            placeholder="What's this page for?"
            className="w-full bg-transparent font-display text-3xl font-bold leading-tight tracking-tight text-ink outline-none placeholder:text-fg-faint focus:outline-none"
          />
        </div>

        <div className="mt-5 flex flex-col gap-2">
          <Label htmlFor="new-page-slug" className="text-fg-subtle">
            URL
          </Label>
          <Input
            id="new-page-slug"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            placeholder={title ? slugifyPage(title) : '/about'}
            className="font-mono"
          />
          <p className="text-xs text-fg-faint">
            Leave blank to derive from the title. Pages live at flat URLs —
            <span className="font-mono"> /about</span> rather than{' '}
            <span className="font-mono">/blog/about</span>.
          </p>
        </div>

        <div className="mt-5 flex flex-col gap-2">
          <Label htmlFor="new-page-status" className="text-fg-subtle">
            Status
          </Label>
          <select
            id="new-page-status"
            value={status}
            onChange={(e) => setStatus(e.target.value as PageStatus)}
            className="rounded-md border border-border bg-paper px-3 py-2 font-sans text-sm text-ink transition-colors focus:border-emerald focus:shadow-focus focus:outline-none"
            data-testid="new-page-status"
          >
            <option value="draft">Draft</option>
            <option value="publish">Publish immediately</option>
            <option value="private">Private</option>
          </select>
        </div>

        {error ? (
          <p
            role="alert"
            data-testid="new-page-error"
            className="mt-5 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-900"
          >
            {error}
          </p>
        ) : null}

        <div className="mt-6 flex items-center justify-end gap-2 border-t border-border pt-5">
          <Button variant="default" asChild>
            <Link href="/pages">Cancel</Link>
          </Button>
          <Button
            type="submit"
            variant="primary"
            disabled={submitting}
            data-testid="new-page-submit"
          >
            {submitting ? (
              <Loader2 aria-hidden="true" width={14} height={14} className="animate-spin" />
            ) : (
              <Plus aria-hidden="true" width={14} height={14} />
            )}
            {submitting ? 'Creating…' : 'Create page'}
          </Button>
        </div>
      </form>
    </section>
  );
}
