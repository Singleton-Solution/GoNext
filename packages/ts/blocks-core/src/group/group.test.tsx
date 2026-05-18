/**
 * `core/group` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { group, GroupEdit, GROUP_INNER_SENTINEL } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/group', () => {
  it('round-trips parse → save with the inner-blocks sentinel intact', () => {
    const attrs = { tagName: 'section' as const };
    const html = group.save({ attributes: attrs });
    expect(html).toContain(GROUP_INNER_SENTINEL);
    expect(html).toMatch(/^<section/);
    expect(group.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ tagName: 'section' });
  });

  it('validates a well-formed group', () => {
    const r = new BlockRegistry();
    r.register(group.definition);
    expect(
      r.validate([
        { type: 'core/group', attributes: { tagName: 'article' } },
      ]).valid,
    ).toBe(true);
  });

  it('rejects an unknown tagName', () => {
    const r = new BlockRegistry();
    r.register(group.definition);
    expect(
      r.validate([
        { type: 'core/group', attributes: { tagName: 'marquee' } },
      ]).valid,
    ).toBe(false);
  });

  it('rejects an unknown layout', () => {
    const r = new BlockRegistry();
    r.register(group.definition);
    expect(
      r.validate([
        { type: 'core/group', attributes: { layout: 'masonry' } },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: default group (div)', () => {
    expect(group.save({ attributes: {} })).toMatchSnapshot();
  });

  it('snapshot: section group with flex layout', () => {
    expect(
      group.save({ attributes: { tagName: 'section', layout: 'flex' } }),
    ).toMatchSnapshot();
  });

  it('serverRender substitutes innerHtml into the sentinel slot', () => {
    const rendered = group.serverRender({}, '<p>child</p>');
    expect(rendered).toContain('<p>child</p>');
    expect(rendered).not.toContain(GROUP_INNER_SENTINEL);
  });

  it('supports.innerBlocks is true so the editor accepts children', () => {
    expect(group.definition.supports?.innerBlocks).toBe(true);
  });

  it('Edit component renders the chosen tag name', () => {
    const { container } = render(
      <GroupEdit
        attributes={{ tagName: 'aside' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="g-1"
        context={{}}
      />,
    );
    expect(container.querySelector('aside[data-block="core/group"]')).not.toBeNull();
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <GroupEdit
        attributes={{ tagName: 'section' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="grp-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
