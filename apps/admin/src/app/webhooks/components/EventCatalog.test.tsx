/**
 * EventCatalog — unit tests.
 *
 * Verifies that the catalog renders the events returned from the API,
 * hides the reserved webhook.test event by default, and toggles
 * selection via the `value`/`onChange` contract.
 */
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const mocks = vi.hoisted(() => ({
  listEventCatalog: vi.fn(),
}));
vi.mock('../actions', () => ({
  listEventCatalog: mocks.listEventCatalog,
}));

import { EventCatalog } from './EventCatalog';

const sample = {
  data: [
    { name: 'webhook.test', description: 'Synthetic event.' },
    { name: 'post.published', description: 'A post was published.' },
    { name: 'comment.created', description: 'A comment was submitted.' },
  ],
};

describe('EventCatalog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.listEventCatalog.mockResolvedValue(sample);
  });

  it('renders catalog entries excluding webhook.test by default', async () => {
    render(<EventCatalog value={new Set()} onChange={vi.fn()} />);
    await waitFor(() => screen.getByText('post.published'));
    expect(screen.getByText('post.published')).toBeInTheDocument();
    expect(screen.getByText('comment.created')).toBeInTheDocument();
    expect(screen.queryByText('webhook.test')).not.toBeInTheDocument();
  });

  it('shows the empty-selection warning when value is empty', async () => {
    render(<EventCatalog value={new Set()} onChange={vi.fn()} />);
    await waitFor(() => screen.getByText('post.published'));
    expect(
      screen.getByText(/will not match any traffic/i),
    ).toBeInTheDocument();
  });

  it('emits a new set on checkbox toggle', async () => {
    const onChange = vi.fn();
    render(<EventCatalog value={new Set()} onChange={onChange} />);
    await waitFor(() => screen.getByText('post.published'));
    fireEvent.click(screen.getByLabelText('Subscribe to post.published'));
    expect(onChange).toHaveBeenCalled();
    const next: ReadonlySet<string> = onChange.mock.calls[0][0];
    expect(next.has('post.published')).toBe(true);
  });

  it('renders a retry button when the catalog fetch fails', async () => {
    mocks.listEventCatalog.mockRejectedValueOnce(new Error('boom'));
    render(<EventCatalog value={new Set()} onChange={vi.fn()} />);
    await waitFor(() => screen.getByText(/Couldn't load events/i));
    expect(screen.getByRole('button', { name: /Retry/ })).toBeInTheDocument();
  });
});
