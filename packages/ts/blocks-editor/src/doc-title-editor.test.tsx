/**
 * Tests for `<DocTitleEditor>` — the headline-style title input.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import {
  DocTitleEditor,
  parsePlaceholder,
} from './doc-title-editor.tsx';

describe('<DocTitleEditor>', () => {
  it('renders the controlled value', () => {
    render(
      <DocTitleEditor
        value="How we source single-origin beans."
        onChange={vi.fn()}
      />,
    );
    const field = screen.getByTestId(
      'doc-title-editor-field',
    ) as HTMLTextAreaElement;
    expect(field.value).toBe('How we source single-origin beans.');
  });

  it('calls onChange on each keystroke', () => {
    const onChange = vi.fn();
    render(<DocTitleEditor value="" onChange={onChange} />);
    const field = screen.getByTestId(
      'doc-title-editor-field',
    ) as HTMLTextAreaElement;
    fireEvent.change(field, { target: { value: 'New title' } });
    expect(onChange).toHaveBeenCalledWith('New title');
  });

  it('shows the italic-accent placeholder when value is empty', () => {
    render(
      <DocTitleEditor
        value=""
        onChange={vi.fn()}
        placeholder="Untitled *draft*."
      />,
    );
    const hint = screen.getByTestId('doc-title-editor-placeholder-hint');
    expect(hint).toBeInTheDocument();
    const accent = screen.getByTestId('doc-title-editor-placeholder-accent');
    expect(accent.tagName).toBe('EM');
    expect(accent).toHaveTextContent('draft');
  });

  it('hides the placeholder hint when value is non-empty', () => {
    render(<DocTitleEditor value="x" onChange={vi.fn()} />);
    expect(
      screen.queryByTestId('doc-title-editor-placeholder-hint'),
    ).toBeNull();
  });

  it('uses an accessible label for screen readers', () => {
    render(<DocTitleEditor value="" onChange={vi.fn()} />);
    expect(
      screen.getByLabelText('Document title'),
    ).toBeInTheDocument();
  });

  it('honors a custom aria-label', () => {
    render(
      <DocTitleEditor value="" onChange={vi.fn()} ariaLabel="Post title" />,
    );
    expect(screen.getByLabelText('Post title')).toBeInTheDocument();
  });

  it('exposes the BEM hooks the editor-theme stylesheet reads', () => {
    render(<DocTitleEditor value="" onChange={vi.fn()} />);
    const root = screen.getByTestId('doc-title-editor');
    expect(root.className).toContain('gonext-doc-title-editor');
    expect(screen.getByTestId('doc-title-editor-field').className).toBe(
      'gonext-doc-title-editor__field',
    );
  });
});

describe('parsePlaceholder', () => {
  it('returns a single plain part for text without accents', () => {
    expect(parsePlaceholder('Untitled draft.')).toEqual([
      { kind: 'plain', text: 'Untitled draft.' },
    ]);
  });

  it('extracts a single accent word from *word*', () => {
    expect(parsePlaceholder('Untitled *draft*.')).toEqual([
      { kind: 'plain', text: 'Untitled ' },
      { kind: 'accent', text: 'draft' },
      { kind: 'plain', text: '.' },
    ]);
  });

  it('extracts multiple accent words', () => {
    expect(
      parsePlaceholder('*One* product for everything you used *five* for.'),
    ).toEqual([
      { kind: 'accent', text: 'One' },
      { kind: 'plain', text: ' product for everything you used ' },
      { kind: 'accent', text: 'five' },
      { kind: 'plain', text: ' for.' },
    ]);
  });

  it('falls back to plain when the text is empty', () => {
    expect(parsePlaceholder('')).toEqual([{ kind: 'plain', text: '' }]);
  });
});
