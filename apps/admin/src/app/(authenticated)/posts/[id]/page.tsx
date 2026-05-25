/**
 * Post detail / edit-metadata — the meta-data side of the post-edit
 * surface. The block editor (which lives inside the same route in
 * v0.3 / N3) opens in a dedicated layout — this page covers the
 * metadata-only edit surface: title, slug, status, scheduling,
 * categories, SEO blurb.
 *
 * Brand treatment ("Living systems"): 1fr / 320px split. The left
 * column carries the editable title (Headline display-type), slug
 * input, and an excerpt textarea on cream paper. The right column
 * is a sidebar inspector — Geist label / Geist Mono value pairs
 * with emerald accents on status pills and a publish CTA at the
 * bottom. Pattern mirrors the right inspector from
 * `docs/design/ui_kits/editor/index.html`.
 *
 * The page is intentionally a thin client component for now: it
 * renders the inspector UI without wiring back to the API. The save
 * action stubs to console; real wiring lands once the PATCH endpoint
 * (issue #76) ships. The architectural goal here is to land the
 * brand surface so subsequent feature PRs can hang real data on it.
 */
'use client';

import type { ReactElement } from 'react';
import { useState } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import {
  Calendar,
  ChevronLeft,
  Eye,
  Globe,
  Save,
  Tag,
  User,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

type PostStatus = 'draft' | 'publish' | 'future' | 'private';

export default function PostDetailPage(): ReactElement {
  const params = useParams<{ id: string }>();
  const postId = params?.id ?? 'new';

  const [title, setTitle] = useState('Untitled post');
  const [slug, setSlug] = useState('untitled-post');
  const [excerpt, setExcerpt] = useState('');
  const [status, setStatus] = useState<PostStatus>('draft');

  return (
    <section data-testid="post-detail" className="flex flex-col gap-6">
      {/* ─── Crumb + page head ─── */}
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
          <div className="flex gap-2">
            <Button variant="default" asChild>
              <Link href="/posts">Cancel</Link>
            </Button>
            <Button
              variant="emerald"
              onClick={() => {
                // Stubbed save — wiring lands with the PATCH endpoint.
                // eslint-disable-next-line no-console
                console.log('[post-detail] save', { postId, title, slug, status });
              }}
            >
              <Save aria-hidden="true" width={14} height={14} />
              Save changes
            </Button>
          </div>
        </div>
      </div>

      {/* ─── Body — 1fr / 320px split ─── */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_320px]">
        {/* Main editor column */}
        <div className="flex flex-col gap-5">
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
          </div>

          <div className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs">
            <Headline as="h2" size="sub" className="text-xl">
              Block <em>editor</em>.
            </Headline>
            <p className="mt-2 text-sm text-fg-muted">
              The block editor opens in a focus mode — title and body live
              there. This metadata surface stays here for quick edits.
            </p>
            <Button variant="default" className="mt-4" asChild>
              <Link href={`/posts/${postId}/edit`}>
                Open block editor →
              </Link>
            </Button>
          </div>
        </div>

        {/* ─── Sidebar inspector ─── */}
        <aside
          className="flex flex-col gap-4"
          aria-label="Post metadata inspector"
        >
          {/* Status panel */}
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
                {status === 'publish' ? (
                  <Badge variant="success" dot>
                    Published
                  </Badge>
                ) : status === 'future' ? (
                  <Badge variant="lavender" dot>
                    Scheduled
                  </Badge>
                ) : status === 'private' ? (
                  <Badge variant="ink" dot>
                    Private
                  </Badge>
                ) : (
                  <Badge dot>Draft</Badge>
                )}
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="status-select" className="text-fg-subtle">
                  Change to
                </Label>
                <select
                  id="status-select"
                  value={status}
                  onChange={(e) => setStatus(e.target.value as PostStatus)}
                  className="rounded-md border border-border bg-paper px-3 py-2 font-sans text-sm text-ink transition-colors focus:border-emerald focus:shadow-focus focus:outline-none"
                >
                  <option value="draft">Draft</option>
                  <option value="publish">Publish now</option>
                  <option value="future">Schedule</option>
                  <option value="private">Private</option>
                </select>
              </div>
            </div>
          </div>

          {/* Schedule panel */}
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
                <span className="font-mono text-xs text-ink-soft">
                  2026-05-25 09:14
                </span>
              </li>
              <li className="flex items-center justify-between">
                <span className="inline-flex items-center gap-2 text-xs font-medium text-fg-subtle">
                  <Calendar aria-hidden="true" width={13} height={13} />
                  Updated
                </span>
                <span className="font-mono text-xs text-ink-soft">
                  Just now
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
                <span className="text-xs text-ink-soft">Mara Wills</span>
              </li>
            </ul>
          </div>

          {/* Tags panel */}
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
                <Badge variant="emerald">
                  <Tag aria-hidden="true" width={10} height={10} />
                  brewing
                </Badge>
                <Badge variant="default">
                  <Tag aria-hidden="true" width={10} height={10} />
                  long-read
                </Badge>
              </div>
              <Input placeholder="Add a tag…" />
            </div>
          </div>

          {/* SEO panel */}
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
