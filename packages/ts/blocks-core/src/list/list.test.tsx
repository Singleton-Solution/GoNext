/**
 * `core/list` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { list, ListEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/list', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { ordered: false, values: ['a', 'b', 'c'] };
    const html = list.save({ attributes: attrs });
    expect(list.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ ordered: false, values: ['a', 'b', 'c'] });
  });

  it('validates a well-formed ordered list', () => {
    const r = new BlockRegistry();
    r.register(list.definition);
    expect(
      r.validate([
        {
          type: 'core/list',
          attributes: { ordered: true, values: ['one', 'two'], start: 3 },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects an instance missing `values`', () => {
    const r = new BlockRegistry();
    r.register(list.definition);
    const result = r.validate([
      { type: 'core/list', attributes: { ordered: false } },
    ]);
    expect(result.valid).toBe(false);
    expect(result.errors[0]?.code).toBe('attributes');
  });

  it('rejects non-string item entries', () => {
    const r = new BlockRegistry();
    r.register(list.definition);
    expect(
      r.validate([
        { type: 'core/list', attributes: { ordered: false, values: [1, 2] } },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: unordered list output', () => {
    expect(
      list.save({ attributes: { ordered: false, values: ['a', 'b'] } }),
    ).toMatchSnapshot();
  });

  it('snapshot: ordered list with start + reversed', () => {
    expect(
      list.save({
        attributes: {
          ordered: true,
          values: ['x', 'y', 'z'],
          start: 5,
          reversed: true,
        },
      }),
    ).toMatchSnapshot();
  });

  it('escapes HTML in each item', () => {
    expect(
      list.save({ attributes: { ordered: false, values: ['<i>x</i>'] } }),
    ).toContain('<li>&lt;i&gt;x&lt;/i&gt;</li>');
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { ordered: true, values: ['a'] };
    expect(list.serverRender(attrs, '')).toBe(list.save({ attributes: attrs }));
  });

  it('Edit component renders items', () => {
    render(
      <ListEdit
        attributes={{ ordered: false, values: ['First', 'Second'] }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="l-1"
        context={{}}
      />,
    );
    expect(screen.getByText('First')).toBeInTheDocument();
    expect(screen.getByText('Second')).toBeInTheDocument();
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <ListEdit
        attributes={{ ordered: false, values: ['First', 'Second', 'Third'] }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="l-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
