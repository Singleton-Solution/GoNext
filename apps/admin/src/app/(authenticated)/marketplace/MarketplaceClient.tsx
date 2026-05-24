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
    marginBottom: 16,
  },
  search: {
    flex: '1 1 240px',
    padding: '8px 10px',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 6,
    fontSize: 14,
  },
  chips: {
    display: 'inline-flex',
    gap: 6,
    flexWrap: 'wrap',
  },
  chip: {
    padding: '4px 10px',
    border: '1px solid var(--color-border, #e4e6ea)',
    borderRadius: 999,
    background: '#ffffff',
    fontSize: 12,
    cursor: 'pointer',
    color: 'var(--color-text, #1c2024)',
  },
  chipActive: {
    background: '#3730a3',
    color: '#ffffff',
    borderColor: '#3730a3',
  },
  grid: {
    display: 'grid',
    gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))',
    gap: 16,
  },
  empty: {
    padding: 32,
    border: '1px dashed var(--color-border, #e4e6ea)',
    borderRadius: 8,
    textAlign: 'center',
    color: 'var(--color-text-muted, #6b7280)',
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
          placeholder="Search plugins…"
          aria-label="Search the marketplace"
          style={styles.search}
        />
        <div style={styles.chips} role="group" aria-label="Filter by category">
          <button
            type="button"
            style={
              category === ''
                ? { ...styles.chip, ...styles.chipActive }
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
                  ? { ...styles.chip, ...styles.chipActive }
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
          {SORT_OPTIONS.map((opt) => (
            <button
              type="button"
              key={opt.value}
              style={
                sort === opt.value
                  ? { ...styles.chip, ...styles.chipActive }
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
