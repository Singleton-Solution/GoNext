/**
 * Admin /search — full-page search results.
 *
 * Reached either from the sidebar GlobalSearch (pressing Enter
 * without a focused result) or via a direct URL like
 * /search?q=hello. Renders the same search.Hit shape the cmd+k
 * popover uses, just laid out as a list rather than a dropdown.
 *
 * Painted in the Living-Systems brand:
 *   - "Search *results*." with the live query rendered as the
 *     editorial italic accent on top of the Headline primitive
 *   - A mono p95 readout that shows the server's query duration
 *     against the 250ms budget the search index targets
 *   - Result cards on paper-2 with emerald-soft <mark> highlights
 *     baked into the excerpt HTML
 *
 * The page is client-rendered for now so the input + result list
 * shares state with the URL via the App Router's
 * useSearchParams hook. A future server-render pass (issue TBD)
 * can pre-paint the initial result set inside <Suspense> the same
 * way the posts list page does.
 */
'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { Suspense, useEffect, useState, type ReactElement } from 'react';
import { Search as SearchIcon, Timer } from 'lucide-react';
import { api, ApiError } from '@/lib/api-client';
import { Headline } from '@/components/ui/headline';
import { Badge } from '@/components/ui/badge';
import type { SearchHit } from '@/components/GlobalSearch';

interface SearchResponse {
  hits: SearchHit[];
  total: number;
  query_duration_ms?: number;
}

const PAGE_SIZE = 50;

// Budget the search index targets per docs/perf/search.md. We pin
// the readout colour to emerald when we beat the budget, warning
// when we exceed it — that visual cue is the only at-a-glance signal
// operators get without opening the metrics dashboard.
const P95_BUDGET_MS = 250;

function hitHref(h: SearchHit): string {
  switch (h.type) {
    case 'post':
      return `/posts/${h.id}`;
    case 'page':
      return `/pages/${h.id}`;
    default:
      return '#';
  }
}

// Next 15 prerenders client pages at build time and refuses to do so for
// any component that reaches for `useSearchParams` outside a Suspense
// boundary (the "missing-suspense-with-csr-bailout" error). The fix is to
// hoist the URL-reading body into a child and wrap the export in
// <Suspense>. Static prerender of the page shell is fine; the params-aware
// inner component bails to client render at request time.
export default function SearchPage(): ReactElement {
  return (
    <Suspense fallback={<EmptyShell />}>
      <SearchPageBody />
    </Suspense>
  );
}

function EmptyShell(): ReactElement {
  return (
    <section className="search-page flex flex-col gap-4" data-testid="search-shell">
      <span className="eyebrow">Find anything</span>
      <Headline as="h1" size="page">
        Search.
      </Headline>
    </section>
  );
}

function SearchPageBody(): ReactElement {
  const params = useSearchParams();
  const q = (params?.get('q') ?? '').trim();

  const [data, setData] = useState<SearchResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (q === '') {
      setData(null);
      setError(null);
      return;
    }
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);
    api
      .get<SearchResponse>(
        `/api/v1/admin/search?q=${encodeURIComponent(q)}&limit=${PAGE_SIZE}`,
        { signal: ctrl.signal },
      )
      .then((res) => {
        if (ctrl.signal.aborted) return;
        setData(res);
      })
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return;
        if (err instanceof ApiError) {
          setError(err.status === 401 ? 'Session expired' : 'Search failed');
        } else {
          setError('Search failed');
        }
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false);
      });
    return () => ctrl.abort();
  }, [q]);

  if (q === '') {
    return (
      <section className="search-page flex flex-col gap-4" data-testid="search-empty">
        <span className="eyebrow">Find anything</span>
        <Headline as="h1" size="page">
          Search.
        </Headline>
        <p className="lead max-w-[58ch]">
          Use the sidebar search box (or press{' '}
          <kbd className="kbd font-mono">⌘K</kbd>) to search posts, pages, and
          users.
        </p>
      </section>
    );
  }

  const duration = data?.query_duration_ms;
  const total = data ? data.total : 0;

  return (
    <section className="search-page flex flex-col gap-5" data-testid="search-results">
      <div className="flex flex-col gap-2">
        <span className="eyebrow">Search results</span>
        <Headline as="h1" size="page">
          Search <em>{q}</em>.
        </Headline>
      </div>

      <div className="flex flex-wrap items-center gap-3 text-sm">
        <Badge variant="default" data-testid="search-total-badge">
          <SearchIcon aria-hidden="true" size={12} />
          {data?.total !== undefined
            ? `${total} result${total === 1 ? '' : 's'}`
            : `${data?.hits.length ?? 0} result${(data?.hits.length ?? 0) === 1 ? '' : 's'}`}
        </Badge>
        {duration !== undefined && (
          <span
            data-testid="p95-readout"
            className={`inline-flex items-center gap-1 font-mono text-xs ${
              duration <= P95_BUDGET_MS ? 'text-emerald-deep' : 'text-warning'
            }`}
          >
            <Timer aria-hidden="true" size={12} />
            <span>p95 budget</span>
            <span className="opacity-80">{duration}ms</span>
            <span className="opacity-60">/ {P95_BUDGET_MS}ms</span>
          </span>
        )}
      </div>

      {loading && <p className="text-sm text-fg-muted">Searching…</p>}
      {error !== null && (
        <p role="alert" className="rounded-md border border-danger/40 bg-danger-soft px-3 py-2 text-sm text-danger">
          {error}
        </p>
      )}

      {data !== null && !loading && (
        <>
          {data.hits.length === 0 ? (
            <div className="card flex flex-col items-center gap-1 py-10 text-center">
              <p className="font-display text-xl font-semibold text-ink">
                No matches.
              </p>
              <p className="text-sm text-fg-muted">
                Try a different keyword or check the post status filter.
              </p>
            </div>
          ) : (
            <ul className="search-page__list" data-testid="search-hit-list">
              {data.hits.map((hit) => (
                <li key={hit.id} className="search-page__hit">
                  <Link href={hitHref(hit)}>
                    <span className="search-page__hit-type">{hit.type}</span>
                    <span className="search-page__hit-title">{hit.title}</span>
                  </Link>
                  {hit.excerpt_html && (
                    <p
                      className="search-page__hit-excerpt"
                      // ExcerptHTML is server-sanitised; see
                      // packages/go/search/highlight.go. The brand's
                      // <mark> rule (globals.css → emerald-soft on
                      // emerald-ink) repaints the highlights with no
                      // additional code here.
                      dangerouslySetInnerHTML={{ __html: hit.excerpt_html }}
                    />
                  )}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}
