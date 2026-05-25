/**
 * Suspended — wrapper-around-Suspense behavioural contract tests.
 *
 * We pin three things:
 *   1. `fallback="card"` (default) — the resolver returns a
 *      <SkeletonCard /> with the supplied srLabel.
 *   2. `fallback="spinner"` — the resolver returns a <LoadingState
 *      variant="spinner">.
 *   3. Custom ReactNode fallback passes through unchanged.
 *
 * We test the `resolveFallback` helper directly so the assertions
 * stay deterministic without needing to actually trigger suspension
 * in jsdom (which is fiddly to set up reliably). Triggering the
 * boundary itself is React-level concern; resolving the fallback
 * is *our* concern.
 *
 * For the integration shape, we render <Suspended> with a child that
 * suspends-then-resolves on a tracked promise, and confirm that
 * children eventually appear.
 */
import { render, screen, act } from '@testing-library/react';
import * as React from 'react';
import { describe, expect, it } from 'vitest';

import { Suspended, resolveFallback } from './Suspended';

describe('resolveFallback()', () => {
  it('returns a SkeletonCard when fallback is "card" or undefined', () => {
    const renderedCard = render(<>{resolveFallback('card', 'Hello')}</>);
    expect(screen.getByTestId('skeleton-card')).toBeInTheDocument();
    expect(
      screen.getByTestId('skeleton-card').querySelector('.sr-only')?.textContent,
    ).toBe('Hello');
    renderedCard.unmount();

    render(<>{resolveFallback(undefined as never, undefined)}</>);
    expect(screen.getByTestId('skeleton-card')).toBeInTheDocument();
  });

  it('returns a spinner LoadingState when fallback is "spinner"', () => {
    render(<>{resolveFallback('spinner', 'Working')}</>);
    const root = screen.getByTestId('loading-state');
    expect(root.getAttribute('data-variant')).toBe('spinner');
    expect(screen.getByTestId('loading-state-label').textContent).toBe(
      'Working',
    );
  });

  it('returns a custom ReactNode unchanged', () => {
    const custom = <div data-testid="custom-fallback">Custom!</div>;
    render(<>{resolveFallback(custom, undefined)}</>);
    expect(screen.getByTestId('custom-fallback').textContent).toBe('Custom!');
  });
});

describe('<Suspended>', () => {
  it('renders children when they have already resolved', () => {
    render(
      <Suspended>
        <div data-testid="payload">ready</div>
      </Suspended>,
    );
    expect(screen.getByTestId('payload').textContent).toBe('ready');
  });

  it('shows the SkeletonCard fallback while a child suspends', async () => {
    // Hand-roll a thenable component to drive Suspense without needing
    // a data-fetching library in the test runtime.
    let resolve: ((value: string) => void) | null = null;
    const promise = new Promise<string>((res) => {
      resolve = res;
    });
    let cached: string | null = null;

    function SuspendingChild(): React.ReactElement {
      if (cached === null) {
        // Throw the in-flight promise — React.Suspense catches it.
        throw promise.then((value) => {
          cached = value;
        });
      }
      return <div data-testid="payload">{cached}</div>;
    }

    render(
      <Suspended srLabel="Loading payload">
        <SuspendingChild />
      </Suspended>,
    );

    // Initial render — fallback is visible.
    expect(screen.getByTestId('skeleton-card')).toBeInTheDocument();
    expect(
      screen.getByTestId('skeleton-card').querySelector('.sr-only')?.textContent,
    ).toBe('Loading payload');

    // Resolve the promise and flush — children swap in.
    await act(async () => {
      resolve?.('ready');
      await promise;
    });

    expect(screen.queryByTestId('skeleton-card')).not.toBeInTheDocument();
    expect(screen.getByTestId('payload').textContent).toBe('ready');
  });
});
