/**
 * Reading settings — page + form tests.
 *
 * Covers the brand restyle (PR following #432):
 *  • the italic-accent display headline ("Reading <em>settings</em>.")
 *  • the four paper-2 section cards (Homepage, Blog index, RSS, Tagline)
 *  • the emerald save button
 *
 * Plus a snapshot of the ReadingForm to lock the field structure in
 * place — any drift in the schema (key, label, type, order) will
 * surface as a snapshot diff.
 *
 * `patchSettings` and `fetchSettings` are mocked at the module level
 * so the test never touches `fetch`.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

const patchMock = vi.fn();
const fetchMock = vi.fn();
vi.mock('../api', () => ({
  patchSettings: (patch: unknown) => patchMock(patch),
  fetchSettings: (group: string) => fetchMock(group),
}));

import { ReadingForm, READING_SCHEMA } from './ReadingForm';
import ReadingSettingsPage from './page';

const FIXTURE = {
  'core.reading.homepage_type': 'latest_posts',
  'core.reading.posts_per_page': 10,
  'core.reading.show_summary': true,
  'core.reading.rss_items': 10,
  'core.reading.rss_full_text': true,
  'core.site.tagline': 'A tagline',
};

describe('ReadingSettingsPage', () => {
  beforeEach(() => {
    patchMock.mockReset();
    fetchMock.mockReset();
  });

  it('renders the brand headline, section cards, and emerald save button', async () => {
    fetchMock.mockResolvedValue({ values: FIXTURE, available: true });
    const page = await ReadingSettingsPage();
    render(page);

    const heading = screen.getByRole('heading', { level: 1, name: /Reading settings/i });
    expect(heading).toBeInTheDocument();
    expect(heading.querySelector('em')?.textContent).toMatch(/settings/i);

    expect(screen.getByRole('heading', { name: /Homepage/i, level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Blog index/i, level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /^RSS$/i, level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Tagline/i, level: 2 })).toBeInTheDocument();

    expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument();
  });

  it('exposes every field declared in READING_SCHEMA', () => {
    render(<ReadingForm initialValues={FIXTURE} />);
    expect(READING_SCHEMA).toHaveLength(7);
    expect(screen.getByLabelText(/Homepage shows/i)).toHaveValue('latest_posts');
    expect(screen.getByLabelText(/Posts per page/i)).toHaveValue(10);
    expect(screen.getByLabelText(/Items in RSS feed/i)).toHaveValue(10);
    expect(screen.getByLabelText(/Tagline/i)).toHaveValue('A tagline');
  });

  it('matches the brand snapshot', () => {
    const { container } = render(<ReadingForm initialValues={FIXTURE} />);
    expect(container.firstChild).toMatchSnapshot();
  });
});
