/**
 * SearchBar — brand-contract snapshot.
 *
 * The search field is the most visible piece of chrome on every docs
 * page. We pin the visual contract:
 *
 *  1. The field carries the `.search-bar__field` class so the paper-3
 *     pill, hover, and focus states defined in styles/docs.css apply.
 *  2. A Lucide search icon (svg) leads the field.
 *  3. A ⌘K kbd hint trails the field.
 *  4. The input is the `search` role, with the documented placeholder.
 *
 * Functional behaviour (fuzzy matching, dropdown opening on focus,
 * keyboard navigation) is already covered by Fuse.js and our own
 * unit tests over `buildSearchIndex`. Here we only guard the markup
 * shape that the brand CSS depends on.
 */
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { SearchBar } from '@/components/SearchBar';
import type { SearchEntry } from '@/lib/content';

const ENTRIES: SearchEntry[] = [
  { section: 'docs', slug: '00-architecture-overview', title: 'Architecture Overview' },
  { section: 'adr', slug: '0001-licensing', title: 'Licensing' },
];

describe('<SearchBar>', () => {
  it('renders the paper-3 pill with a Lucide icon and a kbd hint', () => {
    const { container } = render(<SearchBar entries={ENTRIES} />);

    // Wrapper has the .search-bar landmark.
    const wrapper = container.querySelector('.search-bar');
    expect(wrapper).not.toBeNull();
    expect(wrapper?.getAttribute('role')).toBe('search');

    // The pill itself.
    const field = wrapper?.querySelector('.search-bar__field');
    expect(field).not.toBeNull();

    // Lucide renders an <svg> with a stable class name we can spot.
    const icon = field?.querySelector('svg');
    expect(icon).not.toBeNull();
    expect(icon?.classList.contains('search-bar__icon')).toBe(true);

    // ⌘K kbd hint.
    const kbd = field?.querySelector('.search-bar__kbd');
    expect(kbd?.textContent).toBe('⌘K');
  });

  it('renders an accessible search input with the docs placeholder', () => {
    render(<SearchBar entries={ENTRIES} />);

    const input = screen.getByRole('searchbox', { name: /search documentation/i });
    expect(input).toBeInTheDocument();
    expect(input.getAttribute('placeholder')).toMatch(/search docs/i);
    expect(input.className).toContain('search-bar__input');
  });
});
