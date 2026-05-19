/**
 * Top-bar search — client-only fuzzy index.
 *
 * The server passes us the prebuilt search entries (title + slug + section,
 * no body text) so we don't have to ship the entire docs corpus to the
 * client. Fuse.js handles the fuzzy match; results land in a small dropdown.
 *
 * No analytics, no remote calls — this is a deliberate non-feature: the
 * search index is part of the static bundle and works offline.
 */
'use client';

import Link from 'next/link';
import Fuse from 'fuse.js';
import { useMemo, useState, type ReactElement } from 'react';
import type { SearchEntry } from '@/lib/content';

export function SearchBar({ entries }: { entries: SearchEntry[] }): ReactElement {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);

  const fuse = useMemo(
    () =>
      new Fuse(entries, {
        keys: ['title', 'description'],
        threshold: 0.4,
        ignoreLocation: true,
        minMatchCharLength: 2,
      }),
    [entries],
  );

  const results = useMemo(() => {
    if (query.trim().length < 2) return [];
    return fuse.search(query).slice(0, 8).map((r) => r.item);
  }, [fuse, query]);

  return (
    <div className="search-bar" role="search">
      <input
        type="search"
        placeholder="Search docs..."
        className="search-bar__input"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onFocus={() => setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        aria-label="Search documentation"
      />
      {open && results.length > 0 && (
        <ul className="search-bar__results" role="listbox">
          {results.map((r) => {
            const href = r.slug === '' ? `/${r.section}` : `/${r.section}/${r.slug}`;
            return (
              <li key={`${r.section}-${r.slug}`} className="search-bar__result">
                <Link href={href} className="search-bar__result-link">
                  <span className="search-bar__result-section">{r.section}</span>
                  <span className="search-bar__result-title">{r.title}</span>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
