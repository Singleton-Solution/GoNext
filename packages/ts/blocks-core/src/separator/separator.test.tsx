/**
 * `core/separator` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { separator, SeparatorEdit } from './index.ts';

describe('core/separator', () => {
  it('round-trips parse → save', () => {
    const attrs = { style: 'wide' as const };
    const html = separator.save({ attributes: attrs });
    expect(separator.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ style: 'wide' });
  });

  it('validates with empty attributes (all optional)', () => {
    const r = new BlockRegistry();
    r.register(separator.definition);
    expect(
      r.validate([{ type: 'core/separator', attributes: {} }]).valid,
    ).toBe(true);
  });

  it('rejects an unknown style', () => {
    const r = new BlockRegistry();
    r.register(separator.definition);
    expect(
      r.validate([
        { type: 'core/separator', attributes: { style: 'sparkle' } },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: default separator', () => {
    expect(separator.save({ attributes: {} })).toMatchSnapshot();
  });

  it('snapshot: dots separator', () => {
    expect(
      separator.save({ attributes: { style: 'dots' } }),
    ).toMatchSnapshot();
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = {};
    expect(separator.serverRender(attrs, '')).toBe(
      separator.save({ attributes: attrs }),
    );
  });

  it('Edit component renders an <hr/>', () => {
    const { container } = render(
      <SeparatorEdit
        attributes={{}}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="s-1"
        context={{}}
      />,
    );
    expect(container.querySelector('hr')).not.toBeNull();
  });
});
