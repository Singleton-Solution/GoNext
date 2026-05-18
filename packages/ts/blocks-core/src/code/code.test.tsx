/**
 * `core/code` tests — round-trip, schema validation, and save snapshot.
 */
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import { code, CodeEdit } from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/code', () => {
  it('round-trips parse → save without mutating canonical attributes', () => {
    const attrs = { content: 'const x = 1;', language: 'ts' };
    const html = code.save({ attributes: attrs });
    expect(code.save({ attributes: attrs })).toBe(html);
    expect(attrs).toStrictEqual({ content: 'const x = 1;', language: 'ts' });
  });

  it('validates a well-formed code block', () => {
    const r = new BlockRegistry();
    r.register(code.definition);
    expect(
      r.validate([
        { type: 'core/code', attributes: { content: 'a', language: 'go' } },
      ]).valid,
    ).toBe(true);
  });

  it('rejects a language string with uppercase or punctuation', () => {
    const r = new BlockRegistry();
    r.register(code.definition);
    expect(
      r.validate([
        { type: 'core/code', attributes: { content: 'a', language: 'TS!' } },
      ]).valid,
    ).toBe(false);
  });

  it('snapshot: code without language', () => {
    expect(
      code.save({ attributes: { content: 'echo "hi"' } }),
    ).toMatchSnapshot();
  });

  it('snapshot: code with language', () => {
    expect(
      code.save({ attributes: { content: 'fmt.Println("hi")', language: 'go' } }),
    ).toMatchSnapshot();
  });

  it('escapes HTML special characters in the source', () => {
    expect(
      code.save({ attributes: { content: '<div>&nbsp;</div>' } }),
    ).toContain('&lt;div&gt;&amp;nbsp;&lt;/div&gt;');
  });

  it('server-render parity: matches save() for the same input', () => {
    const attrs = { content: 'x' };
    expect(code.serverRender(attrs, '')).toBe(code.save({ attributes: attrs }));
  });

  it('Edit component renders the placeholder when content is empty', () => {
    render(
      <CodeEdit
        attributes={{ content: '' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="c-1"
        context={{}}
      />,
    );
    expect(screen.getByText('// your code here')).toBeInTheDocument();
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <CodeEdit
        attributes={{ content: 'const x = 1;', language: 'ts' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="c-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
