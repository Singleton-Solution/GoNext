/**
 * CapabilityReview tests.
 *
 * Issue acceptance criteria coverage:
 *  - Every capability from the manifest appears in the review, in
 *    declaration order
 *  - Sensitive caps are visually flagged
 *  - The consent checkbox controls the `acknowledged` state via the
 *    callback
 *  - Unknown capability ids surface as "Unrecognised" rows so an
 *    out-of-band manifest doesn't get silently consented to
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';

import { CapabilityReview } from './CapabilityReview';

describe('CapabilityReview', () => {
  it('renders every capability from the manifest in declaration order', () => {
    const caps = ['email.send', 'posts.read', 'kv.write'];
    render(
      <CapabilityReview
        capabilities={caps}
        acknowledged={false}
        onAcknowledgeChange={() => {}}
      />,
    );

    const list = screen.getByTestId('capability-review-list');
    const items = within(list).getAllByRole('listitem');
    expect(items).toHaveLength(caps.length);

    // The DOM order is the manifest order — not alphabetical, not
    // sensitivity-sorted.
    expect(items[0]).toHaveAttribute('data-capability-id', 'email.send');
    expect(items[1]).toHaveAttribute('data-capability-id', 'posts.read');
    expect(items[2]).toHaveAttribute('data-capability-id', 'kv.write');

    // Each row carries the operator-voice human description.
    expect(items[0]).toHaveTextContent(
      /send transactional email on behalf of this site/i,
    );
    expect(items[1]).toHaveTextContent(/read all posts on this site/i);
    expect(items[2]).toHaveTextContent(
      /write to the plugin’s own key-value namespace/i,
    );
  });

  it('flags sensitive capabilities with a "Sensitive" tag', () => {
    render(
      <CapabilityReview
        capabilities={['email.send', 'posts.read']}
        acknowledged={false}
        onAcknowledgeChange={() => {}}
      />,
    );
    const list = screen.getByTestId('capability-review-list');
    const sensitiveRow = within(list).getByText(/send transactional email/i)
      .closest('li')!;
    const lowRiskRow = within(list).getByText(/read all posts/i).closest('li')!;
    expect(sensitiveRow).toHaveAttribute('data-capability-risk', 'sensitive');
    expect(lowRiskRow).toHaveAttribute('data-capability-risk', 'low');
    expect(within(sensitiveRow).getByText(/sensitive/i)).toBeInTheDocument();
  });

  it('renders the title with the sensitive count when any are present', () => {
    render(
      <CapabilityReview
        capabilities={['posts.read', 'email.send', 'http.fetch']}
        acknowledged={false}
        onAcknowledgeChange={() => {}}
      />,
    );
    expect(
      screen.getByRole('heading', { level: 3, name: /3, 2 sensitive/i }),
    ).toBeInTheDocument();
  });

  it('flags unknown capability ids as Unrecognised', () => {
    render(
      <CapabilityReview
        capabilities={['posts.read', 'not.real']}
        acknowledged={false}
        onAcknowledgeChange={() => {}}
      />,
    );
    const list = screen.getByTestId('capability-review-list');
    const items = within(list).getAllByRole('listitem');
    expect(items[1]).toHaveAttribute('data-capability-id', 'not.real');
    expect(within(items[1] as HTMLElement).getByText(/unrecognised/i)).toBeInTheDocument();
  });

  it('renders an empty-state panel when no capabilities are requested', () => {
    render(
      <CapabilityReview
        capabilities={[]}
        acknowledged={false}
        onAcknowledgeChange={() => {}}
      />,
    );
    expect(
      screen.getByText(/doesn’t request any special permissions/i),
    ).toBeInTheDocument();
    // The consent checkbox is still rendered.
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
  });

  it('invokes onAcknowledgeChange when the checkbox flips', () => {
    const spy = vi.fn();
    render(
      <CapabilityReview
        capabilities={['posts.read']}
        acknowledged={false}
        onAcknowledgeChange={spy}
      />,
    );
    fireEvent.click(screen.getByRole('checkbox'));
    expect(spy).toHaveBeenCalledWith(true);
  });

  it('renders duplicated capabilities once per declaration (verbatim, no dedup)', () => {
    render(
      <CapabilityReview
        capabilities={['posts.read', 'posts.read']}
        acknowledged={false}
        onAcknowledgeChange={() => {}}
      />,
    );
    const items = within(
      screen.getByTestId('capability-review-list'),
    ).getAllByRole('listitem');
    expect(items).toHaveLength(2);
  });
});
