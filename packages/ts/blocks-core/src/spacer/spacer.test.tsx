/**
 * `core/spacer` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { spacer, SpacerEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/spacer', () => {
  it('round-trips parse → save', () => {
    const attrs = { height: 32 };
    const html = spacer.save({ attributes: attrs });
    expect(spacer.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ height: 32 });
  });

  it('validates a well-formed spacer', () => {
    const r = new BlockRegistry();
    r.register(spacer.definition);
    expect(
      r.validate([{ type: 'core/spacer', attributes: { height: 24 } }]).valid,
    ).toBe(true);
  });

  it('rejects negative heights', () => {
    const r = new BlockRegistry();
    r.register(spacer.definition);
    expect(
      r.validate([{ type: 'core/spacer', attributes: { height: -1 } }]).valid,
    ).toBe(false);
  });

  it('rejects heights above the safety ceiling', () => {
    const r = new BlockRegistry();
    r.register(spacer.definition);
    expect(
      r.validate([{ type: 'core/spacer', attributes: { height: 99999 } }]).valid,
    ).toBe(false);
  });

  it('snapshot: spacer at 24px', () => {
    expect(spacer.save({ attributes: { height: 24 } })).toMatchSnapshot();
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { height: 16 };
    expect(spacer.serverRender(attrs, '')).toBe(
      spacer.save({ attributes: attrs }),
    );
  });

  it('Edit component renders a sized div', () => {
    const { container } = render(
      <SpacerEdit
        attributes={{ height: 16 }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="sp-1"
        context={{}}
      />,
    );
    const div = container.querySelector('div[data-block="core/spacer"]');
    expect(div).not.toBeNull();
    expect((div as HTMLElement).style.height).toBe('16px');
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <SpacerEdit
        attributes={{ height: 24 }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="sp-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
