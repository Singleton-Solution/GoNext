/**
 * `core/heading` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { heading, HeadingEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/heading', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { content: 'Title', level: 2 as const, align: 'center' as const };
    const html = heading.save({ attributes: attrs });
    expect(heading.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ content: 'Title', level: 2, align: 'center' });
  });

  it('validates a well-formed level=2 heading', () => {
    const r = new BlockRegistry();
    r.register(heading.definition);
    expect(
      r.validate([
        { type: 'core/heading', attributes: { content: 'hi', level: 2 } },
      ]).valid,
    ).toBe(true);
  });

  it('rejects level=7 (out of HTML range)', () => {
    const r = new BlockRegistry();
    r.register(heading.definition);
    const result = r.validate([
      { type: 'core/heading', attributes: { content: 'hi', level: 7 } },
    ]);
    expect(result.valid).toBe(false);
    expect(result.errors[0]?.code).toBe('attributes');
  });

  it('rejects an anchor with invalid characters', () => {
    const r = new BlockRegistry();
    r.register(heading.definition);
    expect(
      r.validate([
        {
          type: 'core/heading',
          attributes: { content: 'hi', level: 2, anchor: 'Has Space' },
        },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: save output for h2 with no extras', () => {
    expect(
      heading.save({ attributes: { content: 'Section', level: 2 } }),
    ).toMatchSnapshot();
  });

  it('snapshot: save output with anchor + alignment', () => {
    expect(
      heading.save({
        attributes: {
          content: 'Anchored',
          level: 3,
          anchor: 'my-anchor',
          align: 'left',
        },
      }),
    ).toMatchSnapshot();
  });

  it('emits the correct hN tag for each level', () => {
    for (const level of [1, 2, 3, 4, 5, 6] as const) {
      expect(
        heading.save({ attributes: { content: 'x', level } }),
      ).toContain(`<h${level} `);
    }
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { content: 'Same', level: 2 as const };
    expect(heading.serverRender(attrs, '')).toBe(
      heading.save({ attributes: attrs }),
    );
  });

  it('Edit component mounts the correct hN', () => {
    render(
      <HeadingEdit
        attributes={{ content: 'My H3', level: 3 }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="h-1"
        context={{}}
      />,
    );
    expect(screen.getByText('My H3').tagName).toBe('H3');
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <HeadingEdit
        attributes={{ content: 'Section heading', level: 2 }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="h-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
