/**
 * /jobs smoke tests. Pure server-rendered grid — the assertions
 * confirm one card per known queue and that each card links to the
 * filtered DLQ. Issue #507.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';

import JobsPage from './page';
import { KNOWN_QUEUES } from './dlq/types';

describe('JobsPage', () => {
  it('renders without crashing', () => {
    render(<JobsPage />);
    expect(screen.getByTestId('jobs-page')).toBeInTheDocument();
  });

  it('renders the italic-accent headline ("Background jobs.")', () => {
    render(<JobsPage />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.textContent).toMatch(/Background\s+jobs\./);
    expect(h1.querySelector('em')?.textContent).toBe('jobs');
  });

  it('renders one card per known queue', () => {
    render(<JobsPage />);
    const list = screen.getByRole('list', { name: /Queues/i });
    const items = list.querySelectorAll('li');
    expect(items.length).toBe(KNOWN_QUEUES.length);
  });

  it('cards link to /jobs/dlq filtered by queue', () => {
    render(<JobsPage />);
    for (const queue of KNOWN_QUEUES) {
      const card = screen.getByTestId(`jobs-queue-card-${queue}`);
      // Next's <Link> serialises `{pathname, query}` into the rendered
      // href, so we just look for the encoded query param.
      expect(card.getAttribute('href')).toMatch(
        new RegExp(`/jobs/dlq\\?queue=${queue}`),
      );
    }
  });
});
