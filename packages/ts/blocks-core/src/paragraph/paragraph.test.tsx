/**
 * `core/paragraph` tests — round-trip, schema validation, and save snapshot.
 *
 * The same three-piece pattern (round-trip, validate, snapshot) is replayed
 * for every core block. See `../README` / the issue #141 description for
 * the contract these tests pin.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { paragraph, ParagraphEdit } from './index.ts';

describe('core/paragraph', () => {
  it('round-trips parse → save without mutating the canonical attributes', () => {
    const block = {
      type: 'core/paragraph',
      attributes: { content: 'Hello, world.', align: 'center' as const },
    };
    const html = paragraph.save({ attributes: block.attributes });
    // Save is a pure function — re-running it must produce identical bytes.
    expect(paragraph.save({ attributes: block.attributes })).toBe(html);
    // Attributes must round-trip unchanged (no hidden mutation).
    expect(block.attributes).toStrictEqual({
      content: 'Hello, world.',
      align: 'center',
    });
  });

  it('validates a well-formed instance against its schema', () => {
    const r = new BlockRegistry();
    r.register(paragraph.definition);
    const result = r.validate([
      { type: 'core/paragraph', attributes: { content: 'hi', dropCap: true } },
    ]);
    expect(result.valid).toBe(true);
  });

  it('rejects an instance missing the required `content` attribute', () => {
    const r = new BlockRegistry();
    r.register(paragraph.definition);
    const result = r.validate([
      { type: 'core/paragraph', attributes: {} },
    ]);
    expect(result.valid).toBe(false);
    expect(result.errors[0]?.code).toBe('attributes');
  });

  it('rejects an unknown `align` enum value', () => {
    const r = new BlockRegistry();
    r.register(paragraph.definition);
    const result = r.validate([
      {
        type: 'core/paragraph',
        attributes: { content: 'x', align: 'sideways' },
      },
    ]);
    expect(result.valid).toBe(false);
  });

  it('snapshot: save output for a simple paragraph', () => {
    expect(paragraph.save({ attributes: { content: 'Hello' } })).toMatchSnapshot();
  });

  it('snapshot: save output with align + dropCap', () => {
    expect(
      paragraph.save({
        attributes: { content: 'Hi', align: 'right', dropCap: true },
      }),
    ).toMatchSnapshot();
  });

  it('escapes HTML special characters in the content', () => {
    expect(
      paragraph.save({ attributes: { content: '<script>alert(1)</script>' } }),
    ).toBe(
      '<p class="gn-block-paragraph">&lt;script&gt;alert(1)&lt;/script&gt;</p>',
    );
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { content: 'Same bytes', align: 'left' as const };
    expect(paragraph.serverRender(attrs, '')).toBe(
      paragraph.save({ attributes: attrs }),
    );
  });

  it('Edit component renders the content with the data-block attribute', () => {
    render(
      <ParagraphEdit
        attributes={{ content: 'hello' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="p-1"
        context={{}}
      />,
    );
    expect(screen.getByText('hello')).toHaveAttribute(
      'data-block',
      'core/paragraph',
    );
  });
});
