/**
 * Tests for the BreakpointEditor.
 *
 * Coverage:
 *  - One row per breakpoint, four total.
 *  - Editing a px input updates that breakpoint's width.
 *  - Clicking an active-breakpoint button calls onActiveBreakpointChange.
 *  - Clicking "Full width" passes null back to the callback.
 *  - findBreakpoint returns the correct entry by slug.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import {
  BreakpointEditor,
  findBreakpoint,
} from './BreakpointEditor';
import type { Breakpoint } from '../types';

const BREAKPOINTS: Breakpoint[] = [
  { slug: 'sm', name: 'Small', width: 640 },
  { slug: 'md', name: 'Medium', width: 768 },
  { slug: 'lg', name: 'Large', width: 1024 },
  { slug: 'xl', name: 'Extra large', width: 1280 },
];

describe('BreakpointEditor', () => {
  it('renders all four breakpoint inputs', () => {
    render(
      <BreakpointEditor
        breakpoints={BREAKPOINTS}
        onChange={vi.fn()}
        activeBreakpoint={null}
        onActiveBreakpointChange={vi.fn()}
      />,
    );
    for (const bp of BREAKPOINTS) {
      expect(screen.getByTestId(`breakpoint-input-${bp.slug}`)).toBeInTheDocument();
      expect(screen.getByTestId(`breakpoint-active-${bp.slug}`)).toBeInTheDocument();
    }
    expect(screen.getByTestId('breakpoint-active-full')).toBeInTheDocument();
  });

  it('updates a breakpoint width when its input changes', () => {
    const onChange = vi.fn();
    render(
      <BreakpointEditor
        breakpoints={BREAKPOINTS}
        onChange={onChange}
        activeBreakpoint={null}
        onActiveBreakpointChange={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByTestId('breakpoint-input-md'), {
      target: { value: '820' },
    });
    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0][0] as Breakpoint[];
    expect(next[1]?.width).toBe(820);
    expect(next[0]?.width).toBe(640);
  });

  it('clicking an active button calls the active-breakpoint callback', () => {
    const onActive = vi.fn();
    render(
      <BreakpointEditor
        breakpoints={BREAKPOINTS}
        onChange={vi.fn()}
        activeBreakpoint={null}
        onActiveBreakpointChange={onActive}
      />,
    );
    fireEvent.click(screen.getByTestId('breakpoint-active-md'));
    expect(onActive).toHaveBeenCalledWith('md');
  });

  it('clicking "Full width" passes null back', () => {
    const onActive = vi.fn();
    render(
      <BreakpointEditor
        breakpoints={BREAKPOINTS}
        onChange={vi.fn()}
        activeBreakpoint="md"
        onActiveBreakpointChange={onActive}
      />,
    );
    fireEvent.click(screen.getByTestId('breakpoint-active-full'));
    expect(onActive).toHaveBeenCalledWith(null);
  });

  it('marks the currently active breakpoint with aria-pressed=true', () => {
    render(
      <BreakpointEditor
        breakpoints={BREAKPOINTS}
        onChange={vi.fn()}
        activeBreakpoint="lg"
        onActiveBreakpointChange={vi.fn()}
      />,
    );
    expect(
      screen.getByTestId('breakpoint-active-lg').getAttribute('aria-pressed'),
    ).toBe('true');
    expect(
      screen.getByTestId('breakpoint-active-sm').getAttribute('aria-pressed'),
    ).toBe('false');
  });

  it('findBreakpoint returns the matching entry or null', () => {
    expect(findBreakpoint(BREAKPOINTS, 'md')).toEqual({
      slug: 'md',
      name: 'Medium',
      width: 768,
    });
    expect(findBreakpoint(BREAKPOINTS, null)).toBeNull();
  });
});
