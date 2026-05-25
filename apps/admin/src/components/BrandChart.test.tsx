/**
 * BrandChart — visual contract tests.
 *
 * Pins the brand-vocabulary on the new chart primitive:
 *
 *   1. Lavender bars on cream by default; emphasised bar swaps to
 *      emerald (the brand's "this is the headline number" cue).
 *   2. Forest surface paints lavender on a forest-2 well and
 *      tones the axis labels for the dark surface.
 *   3. Bar heights scale relative to the running max so a single
 *      tall bar doesn't squash the rest.
 *
 * The component is presentation-only — these are the only contracts
 * that matter to the rest of the admin.
 */
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { BrandChart } from './BrandChart';

describe('<BrandChart>', () => {
  it('paints lavender bars on cream with an emerald emphasis', () => {
    const { container } = render(
      <BrandChart
        bars={[
          { label: 'p50', value: 100, display: '100' },
          { label: 'p75', value: 200, display: '200', emphasis: true },
          { label: 'p95', value: 350, display: '350' },
        ]}
      />,
    );

    // Two non-emphasised lavender bars + one emerald-tinted emphasised bar.
    const lavender = container.querySelectorAll('.bg-lavender');
    const emerald = container.querySelectorAll('.bg-emerald');
    expect(lavender.length).toBe(2);
    expect(emerald.length).toBe(1);
  });

  it('paints a forest panel when surface is "forest"', () => {
    const { container } = render(
      <BrandChart
        surface="forest"
        bars={[{ label: 'p50', value: 100, display: '100' }]}
      />,
    );
    const wrap = container.querySelector('[data-surface="forest"]');
    expect(wrap).not.toBeNull();
    expect(wrap?.className).toContain('bg-forest-2');
  });

  it('scales bar heights against the running max', () => {
    const { container } = render(
      <BrandChart
        height={100}
        bars={[
          { label: 'a', value: 50, display: '50' },
          { label: 'b', value: 100, display: '100' },
        ]}
      />,
    );
    const bars = container.querySelectorAll('.bg-lavender');
    // The taller bar fills the canvas (~100px). The shorter bar is
    // about half. We assert ordering rather than exact pixels to
    // stay resilient to small floor / rounding changes.
    const heightA = parseFloat((bars[0] as HTMLElement).style.height);
    const heightB = parseFloat((bars[1] as HTMLElement).style.height);
    expect(heightB).toBeGreaterThan(heightA);
  });

  it('emits an accessible label summarising the bar set', () => {
    const { container } = render(
      <BrandChart
        bars={[
          { label: 'p50', value: 100, display: '100ms' },
          { label: 'p75', value: 200, display: '200ms' },
        ]}
      />,
    );
    const root = container.querySelector('[role="img"]');
    expect(root?.getAttribute('aria-label')).toContain('p50 100ms');
    expect(root?.getAttribute('aria-label')).toContain('p75 200ms');
  });
});
