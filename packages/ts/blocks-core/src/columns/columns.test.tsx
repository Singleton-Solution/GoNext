/**
 * `core/columns` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { columns, ColumnsEdit, COLUMNS_INNER_SENTINEL } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/columns', () => {
  it('round-trips parse → save with the inner-blocks sentinel intact', () => {
    const attrs = { columns: 3 };
    const html = columns.save({ attributes: attrs });
    expect(html).toContain(COLUMNS_INNER_SENTINEL);
    expect(columns.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ columns: 3 });
  });

  it('validates a well-formed columns block', () => {
    const r = new BlockRegistry();
    r.register(columns.definition);
    expect(
      r.validate([
        { type: 'core/columns', attributes: { columns: 2 } },
      ]).valid,
    ).toBe(true);
  });

  it('rejects column counts below 2 or above 6', () => {
    const r = new BlockRegistry();
    r.register(columns.definition);
    expect(
      r.validate([
        { type: 'core/columns', attributes: { columns: 1 } },
      ]).valid,
    ).toBe(false);
    expect(
      r.validate([
        { type: 'core/columns', attributes: { columns: 7 } },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: default columns', () => {
    expect(columns.save({ attributes: { columns: 2 } })).toMatchSnapshot();
  });

  it('snapshot: columns with verticalAlignment', () => {
    expect(
      columns.save({
        attributes: { columns: 3, verticalAlignment: 'center' },
      }),
    ).toMatchSnapshot();
  });

  it('serverRender substitutes innerHtml into the sentinel slot', () => {
    const rendered = columns.serverRender({ columns: 2 }, '<p>child</p>');
    expect(rendered).toContain('<p>child</p>');
    expect(rendered).not.toContain(COLUMNS_INNER_SENTINEL);
  });

  it('supports.innerBlocks is true so the editor accepts children', () => {
    expect(columns.definition.supports?.innerBlocks).toBe(true);
  });

  it('Edit component renders the wrapper with the column count class', () => {
    const { container } = render(
      <ColumnsEdit
        attributes={{ columns: 3 }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="c-1"
        context={{}}
      />,
    );
    const root = container.querySelector('div[data-block="core/columns"]');
    expect(root?.className).toContain('gn-block-columns--cols-3');
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <ColumnsEdit
        attributes={{ columns: 2 }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="cols-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
