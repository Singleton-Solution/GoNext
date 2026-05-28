/**
 * Pages list — adapter + page-head tests (issue #506).
 *
 * The page itself is a Server Component that hits the network through
 * `serverApiFetch`, so we can't render the whole tree synchronously
 * here — same constraint the posts list page lives under. Two
 * surfaces are tested instead:
 *
 *   1. `adaptApiPage` — the wire→UI shape adapter. Exercising it in
 *      isolation makes the field-mapping rules easy to pin (status
 *      normalisation, updated-at fallback chain).
 *   2. The brand headline — rendered through a minimal Headline
 *      harness so the italic-accent ("All *pages*.") doesn't drift.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { Headline } from '@/components/ui/headline';
import { adaptApiPage, type ApiPagePost } from './page';

describe('adaptApiPage — wire shape → list-UI shape', () => {
  it('normalises the API "published" status onto the WP-classic "publish" label', () => {
    const apiPage: ApiPagePost = {
      id: 'about',
      title: 'About',
      slug: '/about',
      status: 'published',
      updated_at: '2026-04-10T12:34:56Z',
    };
    expect(adaptApiPage(apiPage).status).toBe('publish');
  });

  it('normalises "scheduled" onto "future" so the badge logic matches the rest of the admin', () => {
    const apiPage: ApiPagePost = {
      id: 'launch',
      title: 'Launch',
      status: 'scheduled',
    };
    expect(adaptApiPage(apiPage).status).toBe('future');
  });

  it('falls back to draft for unknown statuses', () => {
    const apiPage: ApiPagePost = { id: 'x', title: 'X', status: 'bogus' };
    expect(adaptApiPage(apiPage).status).toBe('draft');
  });

  it('prefers updated_at, then published_at, then created_at for the Updated cell', () => {
    expect(
      adaptApiPage({
        id: 'a',
        updated_at: '2026-04-10T00:00:00Z',
        published_at: '2026-01-01T00:00:00Z',
      }).updatedAt,
    ).toBe('2026-04-10T00:00:00Z');

    expect(
      adaptApiPage({
        id: 'b',
        published_at: '2026-02-02T00:00:00Z',
      }).updatedAt,
    ).toBe('2026-02-02T00:00:00Z');

    expect(
      adaptApiPage({
        id: 'c',
        created_at: '2026-03-03T00:00:00Z',
      }).updatedAt,
    ).toBe('2026-03-03T00:00:00Z');
  });

  it('emits "(untitled)" for empty or whitespace titles so the list cell is never blank', () => {
    expect(adaptApiPage({ id: 'x', title: '' }).title).toBe('(untitled)');
    expect(adaptApiPage({ id: 'y', title: '   ' }).title).toBe('(untitled)');
  });

  it('passes the slug through verbatim and defaults to empty string', () => {
    expect(adaptApiPage({ id: 'x', slug: '/about' }).slug).toBe('/about');
    expect(adaptApiPage({ id: 'y' }).slug).toBe('');
  });
});

describe('Pages list page head — brand fixture', () => {
  it('renders the italic-accent headline ("All *pages*.")', () => {
    const { container } = render(
      <Headline as="h1" size="page" className="text-[44px]">
        All <em>pages</em>.
      </Headline>,
    );
    const h1 = container.querySelector('h1');
    expect(h1).not.toBeNull();
    expect(h1?.textContent).toMatch(/All\s+pages\./);
    expect(h1?.querySelector('em')?.textContent).toBe('pages');
  });

  it('matches the page-head snapshot', () => {
    const { container } = render(
      <Headline as="h1" size="page" className="text-[44px]">
        All <em>pages</em>.
      </Headline>,
    );
    expect(container.firstChild).toMatchSnapshot();
  });
});
