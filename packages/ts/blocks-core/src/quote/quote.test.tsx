/**
 * `core/quote` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { quote, QuoteEdit } from './index.ts';

describe('core/quote', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { value: 'A line', citation: 'Author' };
    const html = quote.save({ attributes: attrs });
    expect(quote.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ value: 'A line', citation: 'Author' });
  });

  it('validates a well-formed quote', () => {
    const r = new BlockRegistry();
    r.register(quote.definition);
    expect(
      r.validate([
        { type: 'core/quote', attributes: { value: 'q', citation: 'a' } },
      ]).valid,
    ).toBe(true);
  });

  it('rejects missing `value`', () => {
    const r = new BlockRegistry();
    r.register(quote.definition);
    expect(
      r.validate([{ type: 'core/quote', attributes: {} }]).valid,
    ).toBe(false);
  });

  it('rejects unknown `style` enum', () => {
    const r = new BlockRegistry();
    r.register(quote.definition);
    expect(
      r.validate([
        { type: 'core/quote', attributes: { value: 'x', style: 'huge' } },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: plain quote', () => {
    expect(quote.save({ attributes: { value: 'A line' } })).toMatchSnapshot();
  });

  it('snapshot: large quote with citation', () => {
    expect(
      quote.save({
        attributes: { value: 'A line', citation: 'A. Person', style: 'large' },
      }),
    ).toMatchSnapshot();
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { value: 'x' };
    expect(quote.serverRender(attrs, '')).toBe(quote.save({ attributes: attrs }));
  });

  it('Edit component renders the citation when present', () => {
    render(
      <QuoteEdit
        attributes={{ value: 'A line', citation: 'Author' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="q-1"
        context={{}}
      />,
    );
    expect(screen.getByText('Author')).toBeInTheDocument();
  });
});
