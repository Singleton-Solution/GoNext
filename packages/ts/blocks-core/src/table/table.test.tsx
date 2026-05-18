/**
 * `core/table` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { table, TableEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/table', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = {
      head: [['A', 'B']],
      body: [
        ['1', '2'],
        ['3', '4'],
      ],
    };
    const html = table.save({ attributes: attrs });
    expect(table.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({
      head: [['A', 'B']],
      body: [
        ['1', '2'],
        ['3', '4'],
      ],
    });
  });

  it('validates a well-formed table', () => {
    const r = new BlockRegistry();
    r.register(table.definition);
    expect(
      r.validate([
        {
          type: 'core/table',
          attributes: {
            body: [['1', '2']],
            caption: 'A caption',
            style: { stripes: true, borders: false },
          },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects unknown style key', () => {
    const r = new BlockRegistry();
    r.register(table.definition);
    expect(
      r.validate([
        {
          type: 'core/table',
          attributes: {
            body: [['1']],
            style: { stripes: true, surprise: true },
          },
        },
      ]).valid,
    ).toBe(false);
  });

  it('rejects missing `body`', () => {
    const r = new BlockRegistry();
    r.register(table.definition);
    expect(
      r.validate([{ type: 'core/table', attributes: { caption: 'c' } }]).valid,
    ).toBe(false);
  });

  it('snapshot: minimal table (body only)', () => {
    expect(
      table.save({ attributes: { body: [['1', '2']] } }),
    ).toMatchSnapshot();
  });

  it('snapshot: full table (head + body + foot + caption + styles)', () => {
    expect(
      table.save({
        attributes: {
          head: [['Name', 'Score']],
          body: [
            ['Ada', '99'],
            ['Linus', '88'],
          ],
          foot: [['Total', '187']],
          caption: 'Leaderboard',
          style: { stripes: true, borders: true },
        },
      }),
    ).toMatchSnapshot();
  });

  it('escapes cell content', () => {
    expect(
      table.save({
        attributes: { body: [['<script>alert(1)</script>']] },
      }),
    ).toBe(
      '<table class="gn-block-table"><tbody><tr><td>&lt;script&gt;alert(1)&lt;/script&gt;</td></tr></tbody></table>',
    );
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { body: [['x']] };
    expect(table.serverRender(attrs, '')).toBe(
      table.save({ attributes: attrs }),
    );
  });

  it('Edit component renders the table with header / body / footer', () => {
    const { container } = render(
      <TableEdit
        attributes={{
          head: [['H']],
          body: [['B']],
          foot: [['F']],
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="tbl-1"
        context={{}}
      />,
    );
    expect(container.querySelector('thead th')?.textContent).toBe('H');
    expect(container.querySelector('tbody td')?.textContent).toBe('B');
    expect(container.querySelector('tfoot td')?.textContent).toBe('F');
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <TableEdit
        attributes={{
          head: [['Name', 'Score']],
          body: [
            ['Ada', '99'],
            ['Linus', '88'],
          ],
          caption: 'Leaderboard',
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="tbl-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
