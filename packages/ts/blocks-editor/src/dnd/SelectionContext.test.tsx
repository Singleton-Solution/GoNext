/**
 * Tests for the selection context + click-handler dispatch.
 *
 * The reducer's three intents (replace / toggle / range) have non-
 * trivial behaviour around the anchor, so we test them through the
 * actual hook with a thin harness component. We also cover the
 * `handleSelectionClick` helper directly since it's the canonical
 * modifier-key dispatcher used outside the canvas.
 */
import { describe, expect, it } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import {
  handleSelectionClick,
  SelectionProvider,
  useSelection,
  type SelectionActions,
} from './SelectionContext.tsx';

/**
 * Tiny harness that exposes the selection state as data-attributes on
 * a div and the actions as inline buttons. We can drive the reducer
 * by clicking the buttons and assert on the state attributes.
 */
function Harness() {
  const sel = useSelection();
  return (
    <div
      data-testid="harness"
      data-ids={[...sel.ids].join(',')}
      data-anchor={sel.anchorId ?? ''}
    >
      <button
        data-testid="replace-a"
        onClick={() => sel.replace('a')}
        type="button"
      >
        replace a
      </button>
      <button
        data-testid="toggle-b"
        onClick={() => sel.toggle('b')}
        type="button"
      >
        toggle b
      </button>
      <button
        data-testid="toggle-a"
        onClick={() => sel.toggle('a')}
        type="button"
      >
        toggle a
      </button>
      <button
        data-testid="range-c"
        onClick={() => sel.range('c', ['a', 'b', 'c', 'd'])}
        type="button"
      >
        range c
      </button>
      <button
        data-testid="clear"
        onClick={() => sel.clear()}
        type="button"
      >
        clear
      </button>
    </div>
  );
}

function click(id: string) {
  act(() => {
    screen.getByTestId(id).click();
  });
}

describe('<SelectionProvider> + useSelection', () => {
  it('starts empty by default', () => {
    render(
      <SelectionProvider>
        <Harness />
      </SelectionProvider>,
    );
    const h = screen.getByTestId('harness');
    expect(h.getAttribute('data-ids')).toBe('');
    expect(h.getAttribute('data-anchor')).toBe('');
  });

  it('replace() single-selects and sets the anchor', () => {
    render(
      <SelectionProvider>
        <Harness />
      </SelectionProvider>,
    );
    click('replace-a');
    const h = screen.getByTestId('harness');
    expect(h.getAttribute('data-ids')).toBe('a');
    expect(h.getAttribute('data-anchor')).toBe('a');
  });

  it('toggle() adds and then removes ids and moves the anchor', () => {
    render(
      <SelectionProvider>
        <Harness />
      </SelectionProvider>,
    );
    click('toggle-a');
    expect(screen.getByTestId('harness').getAttribute('data-ids')).toBe('a');
    click('toggle-b');
    expect(screen.getByTestId('harness').getAttribute('data-ids')).toBe(
      'a,b',
    );
    expect(screen.getByTestId('harness').getAttribute('data-anchor')).toBe(
      'b',
    );
    click('toggle-a');
    expect(screen.getByTestId('harness').getAttribute('data-ids')).toBe('b');
    expect(screen.getByTestId('harness').getAttribute('data-anchor')).toBe(
      'a',
    );
  });

  it('range() selects the inclusive slice from anchor → target', () => {
    render(
      <SelectionProvider initialIds={['a']}>
        <Harness />
      </SelectionProvider>,
    );
    // Anchor seeded at 'a'. Range click on 'c' over [a,b,c,d] selects [a,b,c].
    click('range-c');
    expect(screen.getByTestId('harness').getAttribute('data-ids')).toBe(
      'a,b,c',
    );
    // Anchor stays at 'a' (Finder-style; range click does NOT move pivot).
    expect(screen.getByTestId('harness').getAttribute('data-anchor')).toBe(
      'a',
    );
  });

  it('range() with no anchor falls back to replace', () => {
    render(
      <SelectionProvider>
        <Harness />
      </SelectionProvider>,
    );
    click('range-c');
    expect(screen.getByTestId('harness').getAttribute('data-ids')).toBe('c');
    expect(screen.getByTestId('harness').getAttribute('data-anchor')).toBe(
      'c',
    );
  });

  it('clear() resets ids and anchor', () => {
    render(
      <SelectionProvider initialIds={['a', 'b']}>
        <Harness />
      </SelectionProvider>,
    );
    expect(screen.getByTestId('harness').getAttribute('data-ids')).toBe(
      'a,b',
    );
    click('clear');
    expect(screen.getByTestId('harness').getAttribute('data-ids')).toBe('');
    expect(screen.getByTestId('harness').getAttribute('data-anchor')).toBe(
      '',
    );
  });

  it('useSelection throws when used outside a provider', () => {
    // Suppress React's expected error noise so the test output stays clean.
    const errSpy = vi
      .spyOn(console, 'error')
      .mockImplementation(() => undefined);
    expect(() => render(<Harness />)).toThrow(
      /useSelection\(\) called outside/,
    );
    errSpy.mockRestore();
  });
});

describe('handleSelectionClick', () => {
  function buildActions(): SelectionActions & {
    log: { kind: string; id: string; ids?: readonly string[] }[];
  } {
    const log: { kind: string; id: string; ids?: readonly string[] }[] = [];
    return {
      log,
      replace: (id) => log.push({ kind: 'replace', id }),
      toggle: (id) => log.push({ kind: 'toggle', id }),
      range: (id, ids) => log.push({ kind: 'range', id, ids }),
      clear: () => log.push({ kind: 'clear', id: '' }),
    };
  }

  it('plain click maps to replace', () => {
    const actions = buildActions();
    handleSelectionClick(
      { shiftKey: false, metaKey: false, ctrlKey: false },
      'a',
      ['a', 'b'],
      actions,
    );
    expect(actions.log).toEqual([{ kind: 'replace', id: 'a' }]);
  });

  it('Cmd / Ctrl click maps to toggle', () => {
    const actions = buildActions();
    handleSelectionClick(
      { shiftKey: false, metaKey: true, ctrlKey: false },
      'a',
      ['a', 'b'],
      actions,
    );
    handleSelectionClick(
      { shiftKey: false, metaKey: false, ctrlKey: true },
      'b',
      ['a', 'b'],
      actions,
    );
    expect(actions.log).toEqual([
      { kind: 'toggle', id: 'a' },
      { kind: 'toggle', id: 'b' },
    ]);
  });

  it('Shift click maps to range with the ordered ids', () => {
    const actions = buildActions();
    handleSelectionClick(
      { shiftKey: true, metaKey: false, ctrlKey: false },
      'b',
      ['a', 'b', 'c'],
      actions,
    );
    expect(actions.log).toEqual([
      { kind: 'range', id: 'b', ids: ['a', 'b', 'c'] },
    ]);
  });
});
