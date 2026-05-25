/**
 * ErrorState — calm-not-panicked behavioural contract tests.
 *
 * The brand voice is "calibrated, not panicked" — these tests pin
 * the visual choices that carry that voice:
 *
 *   1. Icon tile is **lavender-soft / lavender-deep**, NOT
 *      danger-red. A list that failed to load is not dangerous.
 *   2. Italic accent in the headline is **lavender-deep**, matching
 *      the icon (mood-coordinated, not alarmist).
 *   3. role="alert" so AT users get the interrupting announcement
 *      that errors deserve.
 *   4. Retry CTA renders only when a `retry` callback is provided,
 *      uses the emerald variant, and calls back on click.
 *   5. Optional `code` pill renders above the headline in mono /
 *      lavender-soft when supplied.
 *
 * The component is intentionally not visually parametrised on a
 * danger/red palette — the brand reserves red for *destructive
 * modals*, not for in-place error surfaces. This test pins that
 * decision.
 */
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ErrorState } from './ErrorState';

describe('<ErrorState>', () => {
  it('renders the calm lavender vocabulary with italic-accent title', () => {
    render(
      <ErrorState
        title={
          <>
            Something didn&apos;t <em>respond</em>.
          </>
        }
        body="Our edge in us-east-1 is having a moment. Your draft is saved."
      />,
    );

    const root = screen.getByTestId('error-state');
    // Interrupting announcement on errors.
    expect(root.getAttribute('role')).toBe('alert');

    // Icon tile is lavender, not red.
    const lavenderTile = root.querySelector('.bg-lavender-soft');
    expect(lavenderTile).not.toBeNull();
    expect(root.querySelector('.bg-danger-soft')).toBeNull();
    expect(root.querySelector('.bg-danger')).toBeNull();

    // Title classes pin the italic accent to lavender-deep (NOT
    // danger / red). The class fragments below are the contract.
    const title = screen.getByTestId('error-state-title');
    expect(title.className).toContain('[&_em]:font-serif');
    expect(title.className).toContain('[&_em]:italic');
    expect(title.className).toContain('[&_em]:text-lavender-deep');
    expect(title.querySelector('em')?.textContent).toBe('respond');

    // Body — fg-muted, ~1.55 leading.
    const body = screen.getByTestId('error-state-body');
    expect(body.className).toContain('text-fg-muted');
    expect(body.className).toContain('leading-[1.55]');
  });

  it('renders the retry button when a callback is provided', () => {
    const retry = vi.fn();
    render(<ErrorState title="t" body="b" retry={retry} />);

    const button = screen.getByTestId('error-state-retry');
    expect(button).toBeInTheDocument();
    expect(button.textContent).toContain('Retry');
    // Brand-emerald variant — positive, calming CTA.
    expect(button.className).toContain('bg-emerald');

    fireEvent.click(button);
    expect(retry).toHaveBeenCalledTimes(1);
  });

  it('supports a custom retry label and a secondary action', () => {
    render(
      <ErrorState
        title="t"
        body="b"
        retry={() => {}}
        retryLabel="Try again"
        secondaryAction={
          <a href="https://status.example.com" data-testid="status-link">
            Status page
          </a>
        }
      />,
    );
    expect(screen.getByTestId('error-state-retry').textContent).toContain(
      'Try again',
    );
    expect(screen.getByTestId('status-link')).toBeInTheDocument();
  });

  it('omits both action slots when neither retry nor secondaryAction are passed', () => {
    render(<ErrorState title="t" body="b" />);
    expect(screen.queryByTestId('error-state-actions')).not.toBeInTheDocument();
  });

  it('renders the optional mono error code pill in lavender', () => {
    render(
      <ErrorState
        title="t"
        body="b"
        code="err.503 · us-east-1"
      />,
    );
    const code = screen.getByTestId('error-state-code');
    expect(code.textContent).toContain('err.503 · us-east-1');
    expect(code.className).toContain('font-mono');
    expect(code.className).toContain('bg-lavender-soft');
    expect(code.className).toContain('text-lavender-deep');
  });

  it('forwards arbitrary HTML props to the root', () => {
    render(<ErrorState title="t" body="b" id="oops" aria-label="oops" />);
    const root = screen.getByTestId('error-state');
    expect(root.id).toBe('oops');
    // Note: role="alert" is set by the component and not overridden by
    // arbitrary aria-label — both coexist.
    expect(root.getAttribute('aria-label')).toBe('oops');
  });
});
