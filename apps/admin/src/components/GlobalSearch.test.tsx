/**
 * GlobalSearch component test.
 *
 * Covers the three contracts the spec calls out: debounce
 * (fetcher is not invoked on each keystroke), keyboard navigation
 * (ArrowDown/Up + Enter), and click-to-navigate (mousedown on a
 * result calls router.push).
 *
 * next/navigation is mocked because jsdom does not implement the
 * App Router hooks. The router.push spy lets us assert
 * navigation targets without a real Router.
 *
 * Why no fake timers
 * ------------------
 * We tested fake timers first; combining vi.useFakeTimers() with
 * @testing-library/react's waitFor produces hangs because React's
 * microtask scheduling and vitest's fake-timer queue don't agree on
 * who flushes. Real timers + a small DEBOUNCE_MS + a polling
 * waitFor is simpler and still pins the contract.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { GlobalSearch, type SearchHit } from './GlobalSearch';

const pushSpy = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: pushSpy }),
}));

const fixtureHits: SearchHit[] = [
  {
    id: 'p1',
    type: 'post',
    slug: 'go-rocks',
    title: 'Go programming guide',
    excerpt_html: 'Learn <mark>Go</mark> programming.',
    rank: 0.5,
  },
  {
    id: 'p2',
    type: 'page',
    slug: 'about',
    title: 'About Go',
    excerpt_html: 'About <mark>Go</mark>.',
    rank: 0.4,
  },
];

beforeEach(() => {
  pushSpy.mockClear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('GlobalSearch', () => {
  it('debounces fetches: the fetcher is called once for a burst of input', async () => {
    const fetcher = vi.fn().mockResolvedValue(fixtureHits);
    render(<GlobalSearch fetchHits={fetcher} />);

    const input = screen.getByRole('combobox') as HTMLInputElement;

    fireEvent.change(input, { target: { value: 'g' } });
    fireEvent.change(input, { target: { value: 'go' } });
    fireEvent.change(input, { target: { value: 'go p' } });

    // Wait for the debounce + fetch to settle.
    await waitFor(() => {
      expect(fetcher).toHaveBeenCalled();
    });
    // Exactly one fetch — the earlier keystrokes were swallowed by
    // the debounce.
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0]?.[0]).toBe('go p');
  });

  it('renders hits and supports ArrowDown + Enter to navigate', async () => {
    const fetcher = vi.fn().mockResolvedValue(fixtureHits);
    render(<GlobalSearch fetchHits={fetcher} />);

    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'go' } });

    await waitFor(() => screen.getByRole('listbox'));
    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(2);

    const first = options[0];
    if (!first) throw new Error('expected first option');
    expect(first.getAttribute('aria-selected')).toBe('true');

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    const second = screen.getAllByRole('option')[1];
    if (!second) throw new Error('expected second option');
    expect(second.getAttribute('aria-selected')).toBe('true');

    fireEvent.keyDown(input, { key: 'Enter' });
    expect(pushSpy).toHaveBeenCalledWith('/pages/p2');
  });

  it('Enter with no focused row submits to /search?q=…', async () => {
    const fetcher = vi.fn().mockResolvedValue([] as SearchHit[]);
    render(<GlobalSearch fetchHits={fetcher} />);

    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'kubernetes' } });

    await waitFor(() => expect(fetcher).toHaveBeenCalled());

    fireEvent.keyDown(input, { key: 'Enter' });
    expect(pushSpy).toHaveBeenCalledWith('/search?q=kubernetes');
  });

  it('mousedown on a hit navigates to the hit URL', async () => {
    const fetcher = vi.fn().mockResolvedValue(fixtureHits);
    render(<GlobalSearch fetchHits={fetcher} />);

    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'go' } });

    await waitFor(() => screen.getByRole('listbox'));
    const options = screen.getAllByRole('option');
    const first = options[0];
    if (!first) throw new Error('expected at least one option');

    fireEvent.mouseDown(first);
    expect(pushSpy).toHaveBeenCalledWith('/posts/p1');
  });

  it('Escape closes the dropdown', async () => {
    const fetcher = vi.fn().mockResolvedValue(fixtureHits);
    render(<GlobalSearch fetchHits={fetcher} />);

    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'go' } });

    await waitFor(() => screen.getByRole('listbox'));

    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByRole('listbox')).toBeNull();
  });
});
