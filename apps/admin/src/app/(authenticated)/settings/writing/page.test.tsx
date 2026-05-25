/**
 * Writing settings — page + form tests.
 *
 * Brand restyle coverage: italic-accent headline, three paper-2 section
 * cards (Defaults, Editor, Post by email), emerald save button. Plus a
 * snapshot of the WritingForm to lock the schema-driven field structure.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

const patchMock = vi.fn();
const fetchMock = vi.fn();
vi.mock('../api', () => ({
  patchSettings: (patch: unknown) => patchMock(patch),
  fetchSettings: (group: string) => fetchMock(group),
}));

import { WritingForm, WRITING_SCHEMA } from './WritingForm';
import WritingSettingsPage from './page';

const FIXTURE = {
  'core.writing.default_category': 'uncategorized',
  'core.writing.default_format': 'standard',
  'core.writing.default_editor': 'block',
  'core.writing.post_by_email_enabled': false,
  'core.writing.post_by_email_address': '',
};

describe('WritingSettingsPage', () => {
  beforeEach(() => {
    patchMock.mockReset();
    fetchMock.mockReset();
  });

  it('renders the brand headline, section cards, and emerald save button', async () => {
    fetchMock.mockResolvedValueOnce({ values: FIXTURE, available: true });
    const page = await WritingSettingsPage();
    render(page);

    const heading = screen.getByRole('heading', { level: 1, name: /Writing settings/i });
    expect(heading).toBeInTheDocument();
    expect(heading.querySelector('em')?.textContent).toMatch(/settings/i);

    expect(screen.getByRole('heading', { name: /Defaults/i, level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /^Editor$/i, level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Post by email/i, level: 2 })).toBeInTheDocument();

    expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument();
  });

  it('exposes every field declared in WRITING_SCHEMA', () => {
    render(<WritingForm initialValues={FIXTURE} />);
    expect(WRITING_SCHEMA).toHaveLength(5);
    expect(screen.getByLabelText(/Default category/i)).toHaveValue('uncategorized');
    expect(screen.getByLabelText(/Default post format/i)).toHaveValue('standard');
    expect(screen.getByLabelText(/Default editor/i)).toHaveValue('block');
    expect(screen.getByLabelText(/Enable post by email/i)).not.toBeChecked();
  });

  it('matches the brand snapshot', () => {
    const { container } = render(<WritingForm initialValues={FIXTURE} />);
    expect(container.firstChild).toMatchSnapshot();
  });
});
