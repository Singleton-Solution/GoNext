/**
 * Page detail / edit-metadata — sibling of posts/[id].
 *
 * Pages share the post-type infrastructure (docs/05-admin-api.md §3.1)
 * but the metadata surface trims the bits that don't apply to
 * evergreen content (no scheduling-as-publication, no category
 * taxonomy by default). The brand surface stays identical so the IA
 * is predictable.
 *
 * The block editor for pages opens via the same per-resource route
 * (/pages/[id]/edit); this page is intentionally the metadata-only
 * view so editors can quickly toggle visibility, change the URL, or
 * tweak SEO without entering edit mode.
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
  User,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

type PageStatus = 'draft' | 'publish' | 'private';

export default function PageDetailPage(): ReactElement {
  const params = useParams<{ id: string }>();
  const pageId = params?.id ?? 'new';

  const [title, setTitle] = useState('Untitled page');
  const [slug, setSlug] = useState('/untitled-page');
  const [status, setStatus] = useState<PageStatus>('draft');

  return (
    <section data-testid="page-detail" className="flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <Link
          href="/pages"
          className="inline-flex w-fit items-center gap-1 text-xs font-medium text-fg-subtle hover:text-emerald-deep"
        >
          <ChevronLeft aria-hidden="true" width={13} height={13} />
          Back to pages
        </Link>
        <div className="flex flex-wrap items-end justify-between gap-6 border-b border-border pb-6">
          <div>
            <Headline as="h1" size="page" className="text-[clamp(32px,4vw,44px)]">
              Edit <em>page</em>.
            </Headline>
            <p className="mt-[10px] text-sm text-fg-muted">
              Update the slug, visibility, and SEO blurb.{' '}
              <span className="font-mono text-xs">#{pageId}</span>
            </p>
          </div>
          <div className="flex gap-2">
            <Button variant="default" asChild>
              <Link href="/pages">Cancel</Link>
            </Button>
            <Button
              variant="emerald"
              onClick={() => {
                // eslint-disable-next-line no-console
                console.log('[page-detail] save', { pageId, title, slug, status });
              }}
            >
              <Save aria-hidden="true" width={14} height={14} />
              Save changes
            </Button>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_320px]">
        {/* Main editor column */}
        <div className="flex flex-col gap-5">
          <div className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs">
            <div className="flex flex-col gap-2">
              <Label htmlFor="page-title" className="text-fg-subtle">
                Title
              </Label>
              <input
                id="page-title"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                className="w-full bg-transparent font-display text-3xl font-bold leading-tight tracking-tight text-ink outline-none placeholder:text-fg-faint focus:outline-none"
                placeholder="What's this page about?"
              />
            </div>

            <div className="mt-5 flex flex-col gap-2">
              <Label htmlFor="page-slug" className="text-fg-subtle">
                URL
              </Label>
              <Input
                id="page-slug"
                value={slug}
                onChange={(e) => setSlug(e.target.value)}
                className="font-mono"
              />
            </div>
          </div>

          <div className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs">
            <Headline as="h2" size="sub" className="text-xl">
              Block <em>editor</em>.
            </Headline>
            <p className="mt-2 text-sm text-fg-muted">
              Pages typically have layout-heavy content. The dedicated block
              editor route for pages is not wired yet — use the post editor as
              a model. Until it lands, edit the page&apos;s metadata above and
              push block changes through the API directly.
            </p>
            <Button variant="default" className="mt-4" disabled>
              Block editor — coming soon
            </Button>
          </div>
        </div>

        {/* Sidebar inspector */}
        <aside
          className="flex flex-col gap-4"
          aria-label="Page metadata inspector"
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
                {status === 'publish' ? (
                  <Badge variant="success" dot>
                    Published
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
                <Label htmlFor="page-status" className="text-fg-subtle">
                  Change to
                </Label>
                <select
                  id="page-status"
                  value={status}
                  onChange={(e) => setStatus(e.target.value as PageStatus)}
                  className="rounded-md border border-border bg-paper px-3 py-2 font-sans text-sm text-ink transition-colors focus:border-emerald focus:shadow-focus focus:outline-none"
                >
                  <option value="draft">Draft</option>
                  <option value="publish">Publish now</option>
                  <option value="private">Private</option>
                </select>
              </div>
            </div>
          </div>

          <div className="rounded-lg border border-border bg-paper-2 shadow-xs">
            <div className="border-b border-border px-5 py-3">
              <h3 className="font-sans text-sm font-semibold text-ink">
                Metadata
              </h3>
            </div>
            <ul className="flex flex-col gap-3 p-5 text-sm">
              <li className="flex items-center justify-between">
                <span className="inline-flex items-center gap-2 text-xs font-medium text-fg-subtle">
                  <Calendar aria-hidden="true" width={13} height={13} />
                  Created
                </span>
                <span className="font-mono text-xs text-ink-soft">
                  2026-04-02 11:30
                </span>
              </li>
              <li className="flex items-center justify-between">
                <span className="inline-flex items-center gap-2 text-xs font-medium text-fg-subtle">
                  <Calendar aria-hidden="true" width={13} height={13} />
                  Updated
                </span>
                <span className="font-mono text-xs text-ink-soft">
                  3 days ago
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
                <Label htmlFor="page-seo-title" className="text-fg-subtle">
                  Meta title
                </Label>
                <Input id="page-seo-title" placeholder="Title for search results" />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="page-seo-desc" className="text-fg-subtle">
                  Meta description
                </Label>
                <textarea
                  id="page-seo-desc"
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
