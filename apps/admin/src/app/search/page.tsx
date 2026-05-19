/**
 * Admin /search — full-page search results.
 *
 * Reached either from the sidebar GlobalSearch (pressing Enter
 * without a focused result) or via a direct URL like
 * /search?q=hello. Renders the same search.Hit shape the cmd+k
 * popover uses, just laid out as a list rather than a dropdown.
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
import { useEffect, useState, type ReactElement } from 'react';
import { api, ApiError } from '../api-client';
import type { SearchHit } from '../../components/GlobalSearch';

interface SearchResponse {
  hits: SearchHit[];
  total: number;
  query_duration_ms?: number;
}

const PAGE_SIZE = 50;

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

export default function SearchPage(): ReactElement {
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
      <div className="search-page">
        <h1>Search</h1>
        <p className="muted">
          Use the sidebar search box (or press ⌘K) to search posts, pages, and
          users.
        </p>
      </div>
    );
  }

  return (
    <div className="search-page">
      <h1>
        Search results for <em>{q}</em>
      </h1>
      {loading && <p className="muted">Searching…</p>}
      {error !== null && <p role="alert">{error}</p>}
      {data !== null && !loading && (
        <>
          <p className="muted">
            {data.total >= 0
              ? `${data.total} result${data.total === 1 ? '' : 's'}`
              : `${data.hits.length} result${data.hits.length === 1 ? '' : 's'}`}
          </p>
          {data.hits.length === 0 ? (
            <p>No results for that query.</p>
          ) : (
            <ul className="search-page__list">
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
                      // packages/go/search/highlight.go.
                      dangerouslySetInnerHTML={{ __html: hit.excerpt_html }}
                    />
                  )}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  );
}
