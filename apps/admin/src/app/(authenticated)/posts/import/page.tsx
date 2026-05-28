/**
 * Import posts — explainer / forward to the migration wizard.
 *
 * The "Import" button on the posts list (`/posts` toolbar) used to
 * 404 (issue #507). Bulk import for GoNext lives behind the dedicated
 * migration wizard at `/migrate` — that surface handles WordPress
 * exports (WXR / live REST URL / ACF JSON) and is the right entry
 * point for any meaningful bulk-load.
 *
 * This page therefore does not implement its own import flow; it
 * renders an explainer card that hands off to `/migrate`. The link is
 * the primary CTA so a single click gets the operator where they
 * already wanted to go, while the surrounding copy spells out which
 * sources are supported (so they know to expect a WordPress flow).
 *
 * If a future "drop a CSV here" lightweight importer ships separately
 * from the migration wizard, this is the file it'll grow into.
 *
 * Brand: card + italic-accent headline matching the moodboard. The
 * page-head stays on the same template as siblings so the IA reads
 * predictably.
 */
import Link from 'next/link';
import type { ReactElement } from 'react';
import { ArrowRight, ChevronLeft, Upload } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';

export const metadata = {
  title: 'Import posts · GoNext admin',
};

export default function ImportPostsPage(): ReactElement {
  return (
    <section
      data-testid="import-posts-page"
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
            Import <em>posts</em>.
          </Headline>
          <p className="mt-[10px] max-w-[520px] text-sm text-fg-muted">
            Bulk loads run through the migration wizard. It handles WordPress
            exports, ACF JSON, and a live WP REST URL — with a dry-run
            preview before commit.
          </p>
        </div>
      </div>

      <article className="rounded-lg border border-border bg-paper-2 p-6 shadow-xs">
        <div className="flex items-start gap-4">
          <div
            aria-hidden="true"
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md bg-emerald-soft text-emerald-deep"
          >
            <Upload width={18} height={18} />
          </div>
          <div className="flex flex-col gap-3">
            <Headline as="h2" size="sub" className="text-xl">
              Bulk import goes through <em>Migration</em>.
            </Headline>
            <p className="text-sm text-fg-muted">
              The migration surface walks you through source → options →
              dry-run preview → commit → report. Use it whether you&apos;re
              moving from WordPress or just importing a handful of posts from
              an export file. Everything lands as proper{' '}
              <span className="font-mono text-xs">post</span> rows that the
              regular editor can pick up.
            </p>
            <ul className="ml-4 list-disc text-sm text-fg-muted [&_li]:mt-1">
              <li>WordPress WXR (XML) upload</li>
              <li>Live WordPress REST URL</li>
              <li>ACF JSON export</li>
            </ul>
          </div>
        </div>
        <div className="mt-6 flex flex-wrap items-center justify-end gap-2 border-t border-border pt-5">
          <Button variant="default" asChild>
            <Link href="/posts">Not now</Link>
          </Button>
          <Button variant="primary" asChild>
            <Link href="/migrate" data-testid="import-posts-cta">
              Open the migration wizard
              <ArrowRight aria-hidden="true" width={14} height={14} />
            </Link>
          </Button>
        </div>
      </article>
    </section>
  );
}
