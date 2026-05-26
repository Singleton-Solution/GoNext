'use client';

/**
 * MarketplaceClient — client island for the catalogue.
 *
 * Receives the server-fetched first page of listings and renders the
 * filter chips, search box, and grid. Changes to the filters update
 * the URL via `next/navigation` (so the result is shareable) and
 * trigger a server-side re-fetch through router.refresh().
 *
 * We intentionally don't fan out to the API client-side: keeping the
 * fetch on the server keeps cookie forwarding straightforward and
 * lets server data take precedence over stale client state.
 *
 * Brand
 * =====
 * Search lives inside a cream `--paper-2` shell with an emerald focus
 * halo (`--sh-focus`). Category chips toggle to emerald when active —
 * the "alive" affordance for an applied filter. Sort chips swap to a
 * solid ink fill (the "primary" accent) so the two chip rows don't
 * fight each other for emphasis.
 */

import { useRouter, useSearchParams } from 'next/navigation';
import {
  useCallback,
  useState,
  type ChangeEvent,
  type CSSProperties,
  type ReactElement,
} from 'react';
import { MarketplaceCard } from './components/MarketplaceCard';
import type { ListingCard, SortKey } from './types';

const styles: Record<string, CSSProperties> = {
  toolbar: {
    display: 'flex',
    gap: 12,
    alignItems: 'center',
    flexWrap: 'wrap',
    marginBottom: 24,
  },
  search: {
    flex: '1 1 240px',
    padding: '10px 12px',
    background: 'var(--paper-2)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--r-md)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
    color: 'var(--ink)',
    outline: 'none',
    transition:
      'background var(--dur) var(--ease), border-color var(--dur) var(--ease), box-shadow var(--dur) var(--ease)',
  },
  chipsLabel: {
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    color: 'var(--fg-subtle)',
    fontWeight: 500,
    letterSpacing: '0.04em',
    textTransform: 'uppercase',
    marginRight: 4,
  },
  chips: {
    display: 'inline-flex',
    gap: 6,
    flexWrap: 'wrap',
    alignItems: 'center',
  },
  chip: {
    padding: '5px 12px',
    background: 'var(--paper-2)',
    borderWidth: 1,
    borderStyle: 'solid',
    borderColor: 'var(--border)',
    borderRadius: 'var(--r-pill)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-xs)',
    fontWeight: 500,
    color: 'var(--fg-muted)',
    cursor: 'pointer',
    transition:
      'background var(--dur-fast) var(--ease), color var(--dur-fast) var(--ease), border-color var(--dur-fast) var(--ease)',
  },
  chipActiveEmerald: {
    background: 'var(--emerald-soft)',
    color: 'var(--emerald-deep)',
    borderColor: 'var(--emerald-soft)',
  },
  chipActiveInk: {
    background: 'var(--ink)',
    color: 'var(--paper)',
    borderColor: 'var(--ink)',
  },
  grid: {
    display: 'grid',
    gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))',
    gap: 18,
  },
  empty: {
    padding: 48,
    background: 'var(--paper-2)',
    border: '1px dashed var(--border)',
    borderRadius: 'var(--r-lg)',
    textAlign: 'center',
    color: 'var(--fg-muted)',
    fontFamily: 'var(--font-sans)',
    fontSize: 'var(--t-sm)',
  },
};

const SORT_OPTIONS: ReadonlyArray<{ value: SortKey; label: string }> = [
  { value: 'recent', label: 'Recent' },
  { value: 'stars', label: 'Top rated' },
  { value: 'popular', label: 'Most installed' },
];

export interface MarketplaceClientProps {
  initialListings: ListingCard[];
  initialQuery: string;
  initialCategory: string;
  initialSort: SortKey;
}

export function MarketplaceClient({
  initialListings,
  initialQuery,
  initialCategory,
  initialSort,
}: MarketplaceClientProps): ReactElement {
  const router = useRouter();
  const searchParams = useSearchParams();
  const [q, setQ] = useState(initialQuery);
  const [category, setCategory] = useState(initialCategory);
  const [sort, setSort] = useState<SortKey>(initialSort);

  // Build the union of categories from the server-rendered slice so
  // the chip row reflects what's available. We don't fetch the full
  // list of categories from the API — the catalogue is small enough
  // that the union of the current page is a useful filter signal.
  const categories = Array.from(
    new Set(
      initialListings
        .map((l) => l.primary_category)
        .filter((c): c is string => !!c),
    ),
  ).sort();

  const push = useCallback(
    (next: { q?: string; category?: string; sort?: SortKey }) => {
      const params = new URLSearchParams(searchParams?.toString() ?? '');
      const apply = (k: string, v: string | undefined): void => {
        if (v) params.set(k, v);
        else params.delete(k);
      };
      apply('q', next.q !== undefined ? next.q : q);
      apply('category', next.category !== undefined ? next.category : category);
      apply('sort', next.sort !== undefined ? next.sort : sort);
      const qs = params.toString();
      router.push(qs ? `/marketplace?${qs}` : '/marketplace');
    },
    [router, searchParams, q, category, sort],
  );

  const onSearch = useCallback(
    (e: ChangeEvent<HTMLInputElement>): void => {
      setQ(e.target.value);
    },
    [],
  );

  const onSearchSubmit = useCallback(
    (e: React.FormEvent<HTMLFormElement>): void => {
      e.preventDefault();
      push({ q });
    },
    [push, q],
  );

  return (
    <div data-testid="marketplace-client">
      <form onSubmit={onSearchSubmit} role="search" style={styles.toolbar}>
        <input
          type="search"
          value={q}
          onChange={onSearch}
          placeholder="Search 1,240+ themes & extensions"
          aria-label="Search the marketplace"
          style={styles.search}
        />
        <div style={styles.chips} role="group" aria-label="Filter by category">
          <span style={styles.chipsLabel}>Category</span>
          <button
            type="button"
            style={
              category === ''
                ? { ...styles.chip, ...styles.chipActiveEmerald }
                : styles.chip
            }
            aria-pressed={category === ''}
            onClick={() => {
              setCategory('');
              push({ category: '' });
            }}
          >
            All
          </button>
          {categories.map((c) => (
            <button
              type="button"
              key={c}
              style={
                category === c
                  ? { ...styles.chip, ...styles.chipActiveEmerald }
                  : styles.chip
              }
              aria-pressed={category === c}
              onClick={() => {
                setCategory(c);
                push({ category: c });
              }}
            >
              {c}
            </button>
          ))}
        </div>
        <div style={styles.chips} role="group" aria-label="Sort listings">
          <span style={styles.chipsLabel}>Sort</span>
          {SORT_OPTIONS.map((opt) => (
            <button
              type="button"
              key={opt.value}
              style={
                sort === opt.value
                  ? { ...styles.chip, ...styles.chipActiveInk }
                  : styles.chip
              }
              aria-pressed={sort === opt.value}
              onClick={() => {
                setSort(opt.value);
                push({ sort: opt.value });
              }}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </form>

      {initialListings.length === 0 ? (
        <div style={styles.empty}>
          No listings match these filters. Try clearing the search or
          category chips.
        </div>
      ) : (
        <div style={styles.grid} data-testid="marketplace-grid">
          {initialListings.map((l) => (
            <MarketplaceCard key={l.slug} listing={l} />
          ))}
        </div>
      )}
    </div>
  );
}
