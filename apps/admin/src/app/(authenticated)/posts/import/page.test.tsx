/**
 * /posts/import smoke tests. Pure server-rendered explainer — the
 * assertions confirm the route renders and that the CTA points at the
 * migration wizard so the original click-target intent (bulk import)
 * is preserved. Issue #507.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';

import ImportPostsPage from './page';

describe('ImportPostsPage', () => {
  it('renders without crashing', () => {
    render(<ImportPostsPage />);
    expect(screen.getByTestId('import-posts-page')).toBeInTheDocument();
  });

  it('renders the italic-accent headline ("Import posts.")', () => {
    render(<ImportPostsPage />);
    const h1 = screen.getByRole('heading', { level: 1 });
    expect(h1.textContent).toMatch(/Import\s+posts\./);
    expect(h1.querySelector('em')?.textContent).toBe('posts');
  });

  it('routes the primary CTA to the migration wizard', () => {
    render(<ImportPostsPage />);
    const cta = screen.getByTestId('import-posts-cta');
    expect(cta).toHaveAttribute('href', '/migrate');
  });
});
