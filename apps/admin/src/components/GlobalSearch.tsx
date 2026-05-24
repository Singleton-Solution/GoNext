'use client';

/**
 * GlobalSearch — admin shell's always-visible search input.
 *
 * Lives in the sidebar header (above the nav list). Behaves like a
 * popover / mini-command palette:
 *
 *   - A text input is visible at all times.
 *   - Typing into the input (debounced, see {@link DEBOUNCE_MS})
 *     fires a GET /api/v1/admin/search request and renders a
 *     dropdown of live results below the input.
 *   - Cmd+K / Ctrl+K from anywhere in the admin focuses the input
 *     (no separate modal — the input is already there).
 *   - Arrow up/down navigates the result list; Enter activates the
 *     focused row.
 *   - Hitting Enter when no row is focused submits to /search?q=…
 *     for the full-page result view.
 *
 * The component is intentionally self-contained: it owns the
 * AbortController used to cancel in-flight requests as the user
 * types, the debounce timer, and the focused-index state. Lifting
 * any of these into a Zustand/Redux store would be premature for
 * what is essentially one input + one list.
 */

import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { ChangeEvent, KeyboardEvent, ReactElement } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { api, ApiError } from '@/lib/api-client';

// DEBOUNCE_MS is the input-to-fetch delay. Tuned for fast typists:
// 200 ms is short enough that the dropdown feels live, long enough
// that a five-character word doesn't fan out into five HTTP
// requests. The /api/v1/admin/search endpoint is cheap enough that
// we could go lower, but the throttling-via-debounce also keeps
// noise out of the logs.
export const DEBOUNCE_MS = 200;

// MAX_RESULTS is the dropdown cap. Anything beyond this scrolls
// off-screen for a 720p laptop; users who want more click through
// to the full /search page.
const MAX_RESULTS = 10;

/**
 * Hit mirrors the JSON shape from search.Hit on the Go side. The
 * decoupling is deliberate: we only type the fields this component
 * reads, so additive backend changes don't break the client.
 */
export interface SearchHit {
  id: string;
  type: string;
  slug: string;
  title: string;
  excerpt_html: string;
  rank: number;
  matched_terms?: string[];
}

interface SearchResponse {
  hits: SearchHit[];
  total: number;
}

/**
 * Compute the admin route for a given hit. Posts and pages have
 * dedicated edit screens; unknown types route to the global
 * /search page as a graceful fallback. We do NOT hand-craft URLs
 * here — every section of the admin is responsible for its own
 * routing, and breaking that contract for the search overlay would
 * make the IA harder to evolve.
 */
function hitHref(h: SearchHit): string {
  switch (h.type) {
    case 'post':
      return `/posts/${h.id}`;
    case 'page':
      return `/pages/${h.id}`;
    default:
      return `/search?q=${encodeURIComponent(h.title)}`;
  }
}

export interface GlobalSearchProps {
  /**
   * Optional: override the fetcher (testing seam). Defaults to a
   * call against the configured API base.
   */
  fetchHits?: (q: string, signal: AbortSignal) => Promise<SearchHit[]>;
}

async function defaultFetchHits(q: string, signal: AbortSignal): Promise<SearchHit[]> {
  const path = `/api/v1/admin/search?q=${encodeURIComponent(q)}&limit=${MAX_RESULTS}`;
  try {
    const body = await api.get<SearchResponse>(path, { signal });
    return body.hits ?? [];
  } catch (err) {
    if (err instanceof ApiError) {
      // 4xx other than 401 is "no results" from the user's
      // perspective. 401 means the session expired — surface it
      // by letting the page redirect on the next click rather than
      // throwing here, which would scrape ugly errors into the
      // console.
      if (err.status >= 400 && err.status < 500) {
        return [];
      }
    }
    if ((err as { name?: string })?.name === 'AbortError') {
      return [];
    }
    throw err;
  }
}

export function GlobalSearch(
  { fetchHits = defaultFetchHits }: GlobalSearchProps = {},
): ReactElement {
  const router = useRouter();
  const inputId = useId();
  const listboxId = useId();

  const [query, setQuery] = useState('');
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [open, setOpen] = useState(false);
  const [focused, setFocused] = useState(-1);
  const [loading, setLoading] = useState(false);

  const inputRef = useRef<HTMLInputElement | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Cmd+K / Ctrl+K — global focus. We attach the listener once and
  // detach on unmount; the listener is small enough that the
  // dependency array stays empty without lint pain.
  useEffect(() => {
    function onKey(ev: globalThis.KeyboardEvent): void {
      if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === 'k') {
        ev.preventDefault();
        inputRef.current?.focus();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Debounced fetch. We cancel any in-flight request whenever the
  // query changes — the abort guarantees we don't render stale
  // results when the user types faster than the network.
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    if (abortRef.current) abortRef.current.abort();

    const trimmed = query.trim();
    if (trimmed === '') {
      setHits([]);
      setOpen(false);
      setLoading(false);
      return;
    }

    setLoading(true);
    debounceRef.current = setTimeout(async () => {
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      try {
        const next = await fetchHits(trimmed, ctrl.signal);
        // Guard against late deliveries from a prior request.
        if (ctrl.signal.aborted) return;
        setHits(next);
        setOpen(true);
        setFocused(next.length > 0 ? 0 : -1);
      } catch {
        // Failure is silent from the user's perspective — the
        // sidebar input must never explode the whole layout. The
        // ApiError branch in defaultFetchHits already handled
        // expected 4xx; this catches network errors / 5xx.
        setHits([]);
        setOpen(false);
      } finally {
        setLoading(false);
      }
    }, DEBOUNCE_MS);

    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [query, fetchHits]);

  const handleChange = useCallback((ev: ChangeEvent<HTMLInputElement>) => {
    setQuery(ev.target.value);
  }, []);

  const handleKeyDown = useCallback(
    (ev: KeyboardEvent<HTMLInputElement>) => {
      if (!open && (ev.key === 'ArrowDown' || ev.key === 'ArrowUp')) {
        if (hits.length > 0) setOpen(true);
        return;
      }
      switch (ev.key) {
        case 'ArrowDown':
          ev.preventDefault();
          setFocused((i) => Math.min(i + 1, hits.length - 1));
          break;
        case 'ArrowUp':
          ev.preventDefault();
          setFocused((i) => Math.max(i - 1, 0));
          break;
        case 'Enter': {
          ev.preventDefault();
          if (focused >= 0 && hits[focused]) {
            router.push(hitHref(hits[focused]));
            setOpen(false);
            return;
          }
          const trimmed = query.trim();
          if (trimmed !== '') {
            router.push(`/search?q=${encodeURIComponent(trimmed)}`);
            setOpen(false);
          }
          break;
        }
        case 'Escape':
          setOpen(false);
          setFocused(-1);
          break;
      }
    },
    [open, hits, focused, query, router],
  );

  return (
    <div className="global-search" role="search">
      <label className="visually-hidden" htmlFor={inputId}>
        Search posts, pages, and users
      </label>
      <input
        id={inputId}
        ref={inputRef}
        type="search"
        className="global-search__input"
        placeholder="Search…  (⌘K)"
        value={query}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        onFocus={() => {
          if (hits.length > 0) setOpen(true);
        }}
        onBlur={() => {
          // Delay so a click on a result lands before the dropdown
          // closes. 120 ms matches the next/link prefetch cadence
          // and is short enough that an accidental blur recovers.
          window.setTimeout(() => setOpen(false), 120);
        }}
        autoComplete="off"
        spellCheck={false}
        aria-controls={listboxId}
        aria-expanded={open}
        aria-autocomplete="list"
        aria-activedescendant={
          focused >= 0 && hits[focused] ? `gs-hit-${hits[focused].id}` : undefined
        }
        role="combobox"
      />
      {open && (
        <ul
          id={listboxId}
          className="global-search__results"
          role="listbox"
          aria-label="Search results"
        >
          {hits.length === 0 && !loading && (
            <li className="global-search__empty" role="presentation">
              No results
            </li>
          )}
          {hits.map((hit, idx) => (
            <li
              key={hit.id}
              id={`gs-hit-${hit.id}`}
              role="option"
              aria-selected={idx === focused}
              className={
                idx === focused
                  ? 'global-search__hit global-search__hit--focused'
                  : 'global-search__hit'
              }
              // We listen to mousedown rather than click because
              // mousedown fires before the input's onBlur — without
              // this the dropdown would close before the click
              // reached the link.
              onMouseDown={(ev) => {
                ev.preventDefault();
                router.push(hitHref(hit));
                setOpen(false);
              }}
            >
              <Link href={hitHref(hit)} tabIndex={-1}>
                <span className="global-search__hit-type">{hit.type}</span>
                <span className="global-search__hit-title">{hit.title}</span>
                {hit.excerpt_html && (
                  <span
                    className="global-search__hit-excerpt"
                    // ExcerptHTML is server-sanitised by
                    // packages/go/search/highlight.go — only
                    // <mark> tags pass through, everything else
                    // is HTML-escaped. See that file's safety
                    // contract.
                    dangerouslySetInnerHTML={{ __html: hit.excerpt_html }}
                  />
                )}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
