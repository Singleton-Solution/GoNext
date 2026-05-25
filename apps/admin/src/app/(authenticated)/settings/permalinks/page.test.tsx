/**
 * Permalinks settings — page + form tests.
 *
 * Brand restyle coverage:
 *  • italic-accent headline ("Permalink <em>structure</em>.")
 *  • paper-2 "Custom structure" + "Structure presets" cards
 *  • paper-3 radio cards with the mono URL preview per preset
 *  • picking a preset writes its format into the underlying input
 *  • the live preview replaces tokens with sample values
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';

const patchMock = vi.fn();
const fetchMock = vi.fn();
vi.mock('../api', () => ({
  patchSettings: (patch: unknown) => patchMock(patch),
  fetchSettings: (group: string) => fetchMock(group),
}));

import { PermalinksForm, PERMALINKS_SCHEMA } from './PermalinksForm';
import PermalinksSettingsPage from './page';

const FIXTURE = {
  'core.permalinks.format': '/%year%/%monthnum%/%postname%',
};

describe('PermalinksSettingsPage', () => {
  beforeEach(() => {
    patchMock.mockReset();
    fetchMock.mockReset();
  });

  it('renders the brand headline, preset cards, and emerald save button', async () => {
    fetchMock.mockResolvedValueOnce({ values: FIXTURE, available: true });
    const page = await PermalinksSettingsPage();
    render(page);

    const heading = screen.getByRole('heading', { level: 1, name: /Permalink structure/i });
    expect(heading).toBeInTheDocument();
    expect(heading.querySelector('em')?.textContent).toMatch(/structure/i);

    expect(screen.getByRole('heading', { name: /Custom structure/i, level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Structure presets/i, level: 2 })).toBeInTheDocument();

    // Five preset radio cards, each with a mono URL preview.
    const presets = screen.getByTestId('permalinks-presets');
    expect(within(presets).getAllByRole('radio')).toHaveLength(5);
    expect(within(presets).getByText('/hello-world')).toBeInTheDocument();
    expect(within(presets).getByText('/archives/123')).toBeInTheDocument();

    expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument();
  });

  it('exposes a single mono-styled custom-structure field', () => {
    render(<PermalinksForm initialValues={FIXTURE} />);
    expect(PERMALINKS_SCHEMA).toHaveLength(1);
    const input = screen.getByLabelText(/Custom structure/i);
    expect(input).toHaveValue('/%year%/%monthnum%/%postname%');
    expect(input.closest('.form-field')).toHaveClass('form-field--mono');
  });

  it('picking a preset rewrites the custom-structure value', () => {
    render(<PermalinksForm initialValues={FIXTURE} />);
    const presets = screen.getByTestId('permalinks-presets');
    fireEvent.click(within(presets).getByRole('radio', { name: /Post name/i }));
    expect(screen.getByLabelText(/Custom structure/i)).toHaveValue('/%postname%');
  });

  it('renders the live URL preview from the format string', () => {
    render(<PermalinksForm initialValues={FIXTURE} />);
    expect(screen.getByTestId('permalinks-preview')).toHaveTextContent(
      '/2026/05/hello-world',
    );
  });

  it('matches the brand snapshot', () => {
    const { container } = render(<PermalinksForm initialValues={FIXTURE} />);
    expect(container.firstChild).toMatchSnapshot();
  });
});
