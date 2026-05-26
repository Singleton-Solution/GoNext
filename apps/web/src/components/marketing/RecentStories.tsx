/**
 * Recent stories — the section that surfaces the latest posts from the
 * API on the public landing. This is what preserves the home page's
 * original archive behavior (showing latest posts) while wrapping it
 * in the brand surface.
 *
 * Each row uses the post-meta scale from the kit: a small emerald
 * eyebrow ("the most recent"), then a heavy Archivo headline, a sub
 * line in Geist body, and a fine-print mono date + author. Empty
 * state mirrors the kit's "first-run" empty card with a one-line
 * encouragement.
 */
import Link from 'next/link';
import { ArrowRight, Clock } from 'lucide-react';
import type { ReactElement } from 'react';

import { Headline } from '@/components/brand/Headline';
import type { Post } from '@/lib/api';

interface RecentStoriesProps {
  posts: ReadonlyArray<Post>;
}

function formatDate(iso?: string): string {
  if (!iso) return '';
  // Format without locale so server + client stay stable for snapshots.
  // The kit uses "Aug 14" style — we mirror that with toLocaleDateString
  // in the 'en-US' locale.
  try {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  } catch {
    return '';
  }
}

export function MarketingRecentStories({
  posts,
}: RecentStoriesProps): ReactElement {
  return (
    <section className="py-[120px]">
      <div className="mx-auto max-w-[1240px] px-8">
        <div className="mb-14 flex flex-wrap items-end justify-between gap-4">
          <div className="max-w-[640px]">
            <span className="mb-3.5 inline-block text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
              From the network
            </span>
            <Headline size="section">
              Recent <em>stories</em> from sites running on GoNext.
            </Headline>
          </div>
          <Link
            href="/feed"
            className="inline-flex items-center gap-1.5 text-sm font-medium text-emerald-deep no-underline transition-colors duration-DEFAULT ease-brand hover:text-ink"
          >
            Subscribe to the feed
            <ArrowRight className="size-[13px]" aria-hidden />
          </Link>
        </div>

        {posts.length === 0 ? (
          <div className="rounded-lg border border-border bg-paper-2 p-9 text-center">
            <p className="text-base text-fg-muted">
              No published posts yet — be the first to{' '}
              <Link
                href="/start"
                className="text-emerald-deep no-underline underline-offset-2 hover:underline"
              >
                start a site
              </Link>
              .
            </p>
          </div>
        ) : (
          <ul className="grid gap-5 md:grid-cols-2 lg:grid-cols-3">
            {posts.slice(0, 6).map((post) => {
              const href = `/${encodeURIComponent(post.slug)}`;
              const date = formatDate(post.publishedAt);
              return (
                <li key={post.id}>
                  <Link
                    href={href}
                    className="group flex h-full flex-col gap-3 rounded-lg border border-border bg-paper-2 p-6 no-underline transition-colors duration-DEFAULT ease-brand hover:border-border-strong hover:bg-paper-3"
                  >
                    <div className="flex items-center gap-2 text-2xs font-medium uppercase tracking-[0.1em] text-fg-subtle">
                      <Clock className="size-3 text-emerald-deep" aria-hidden />
                      {date || 'Recent'}
                      {post.authorName ? (
                        <>
                          <span aria-hidden>·</span>
                          <span className="normal-case tracking-normal text-fg-muted">
                            {post.authorName}
                          </span>
                        </>
                      ) : null}
                    </div>
                    <h3 className="font-display text-xl font-bold leading-[1.15] tracking-tight text-ink">
                      {post.title}
                    </h3>
                    {post.excerpt ? (
                      <p className="line-clamp-3 text-sm leading-[1.5] text-fg-muted">
                        {post.excerpt}
                      </p>
                    ) : null}
                    <span className="mt-auto inline-flex items-center gap-1.5 text-sm font-medium text-emerald-deep transition-transform duration-DEFAULT ease-brand group-hover:translate-x-0.5">
                      Read story
                      <ArrowRight className="size-[13px]" aria-hidden />
                    </span>
                  </Link>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </section>
  );
}
