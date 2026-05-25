/**
 * RedirectForm — brand-restyle contract tests.
 *
 * Pins the brand-token shapes consumers depend on:
 *
 *   1. Source + destination inputs carry the `font-mono` class (the
 *      paths read as code, not as prose).
 *   2. The regex playground only renders when the toggle is on, and
 *      the sample chips are paper-bound mono buttons.
 *   3. A successful regex test wraps each captured group in an
 *      <mark> styled with bg-emerald-soft / text-emerald-deep.
 */
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { RedirectForm } from './RedirectForm';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn(), refresh: vi.fn() }),
}));

const testRegexMock = vi.fn();
vi.mock('./actions', () => ({
  createRedirect: vi.fn(),
  updateRedirect: vi.fn(),
  testRegex: (...args: unknown[]) => testRegexMock(...args),
}));

describe('<RedirectForm>', () => {
  it('renders the source + destination inputs in mono', () => {
    render(<RedirectForm />);

    const source = screen.getByTestId('source-input');
    const destination = screen.getByTestId('destination-input');
    expect(source.className).toContain('font-mono');
    expect(destination.className).toContain('font-mono');
  });

  it('hides the regex playground until the switch is toggled on', () => {
    render(<RedirectForm />);

    expect(screen.queryByTestId('regex-playground')).toBeNull();
    const switchEl = screen.getByTestId('regex-switch');
    fireEvent.click(switchEl);
    expect(screen.getByTestId('regex-playground')).not.toBeNull();
  });

  it('paints captured groups with the emerald-soft <mark> highlight', async () => {
    testRegexMock.mockResolvedValueOnce({
      compiles: true,
      matches: true,
      captures: ['hello-world'],
      destination: '/posts/hello-world',
    });
    render(<RedirectForm />);

    // Toggle regex mode on.
    fireEvent.click(screen.getByTestId('regex-switch'));

    // Populate inputs.
    fireEvent.change(screen.getByTestId('source-input'), {
      target: { value: '^/blog/(.+)$' },
    });
    fireEvent.change(screen.getByTestId('destination-input'), {
      target: { value: '/posts/$1' },
    });
    fireEvent.change(screen.getByTestId('sample-input'), {
      target: { value: '/blog/hello-world' },
    });

    fireEvent.click(screen.getByTestId('test-regex'));

    await waitFor(() => {
      expect(screen.getByTestId('regex-result')).not.toBeNull();
    });

    const highlight = screen
      .getByTestId('regex-match-highlights')
      .querySelector('mark');
    expect(highlight).not.toBeNull();
    expect(highlight?.className).toContain('bg-emerald-soft');
    expect(highlight?.className).toContain('text-emerald-deep');
    expect(highlight?.textContent).toBe('hello-world');
  });

  it('exposes paper-3 sample chips inside the playground', () => {
    render(<RedirectForm />);
    fireEvent.click(screen.getByTestId('regex-switch'));

    const chip = screen.getByTestId('sample-chip-/blog/hello-world');
    expect(chip.tagName).toBe('BUTTON');
    expect(chip.className).toContain('font-mono');
  });
});
