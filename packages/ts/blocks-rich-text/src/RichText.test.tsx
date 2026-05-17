/**
 * `<RichText/>` component tests.
 *
 * The full keyboard-driven Lexical interaction set lives in browser-level
 * Playwright tests downstream (see `apps/admin/e2e/`). At the unit level
 * we pin the contract that matters to a host block:
 *
 *   - The component mounts with a non-empty role="textbox" descendant.
 *   - The accessibility props (`aria-label`, `aria-placeholder`) reach
 *     the contenteditable element.
 *   - Seed `value` (string or run array) lands in the editor body.
 *   - The wrapper carries the `data-block` attribute and the supplied
 *     className so host blocks can target it from CSS / queries.
 *
 * Lexical bootstraps async via microtasks; we use RTL's `findBy*` and
 * `waitFor` to let the editor settle before asserting.
 */
import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { RichText } from './RichText.tsx';
import { text } from './inline.ts';

describe('<RichText />', () => {
  it('renders a role=textbox surface', async () => {
    render(<RichText value="" placeholder="Type here…" />);
    const textbox = await screen.findByRole('textbox');
    expect(textbox).toBeInTheDocument();
  });

  it('forwards aria-label and aria-placeholder onto the editable surface', async () => {
    render(
      <RichText
        value=""
        placeholder="Hint"
        ariaLabel="Block content"
      />,
    );
    const textbox = await screen.findByRole('textbox');
    expect(textbox).toHaveAttribute('aria-label', 'Block content');
    expect(textbox).toHaveAttribute('aria-placeholder', 'Hint');
  });

  it('places the wrapper className and data-block attribute', async () => {
    const { container } = render(
      <RichText
        value="hi"
        className="my-paragraph"
        dataBlock="core/paragraph"
      />,
    );
    await screen.findByRole('textbox');
    const wrapper = container.querySelector('[data-rich-text-root]');
    expect(wrapper).not.toBeNull();
    expect(wrapper).toHaveClass('my-paragraph');
    expect(wrapper).toHaveAttribute('data-block', 'core/paragraph');
  });

  it('seeds the editor with a plain-string value', async () => {
    render(<RichText value="hello world" />);
    const textbox = await screen.findByRole('textbox');
    await waitFor(() => {
      expect(textbox.textContent).toContain('hello world');
    });
  });

  it('seeds the editor with an InlineRun[] value', async () => {
    render(
      <RichText
        value={[text('bold', { bold: true }), text(' and plain')]}
      />,
    );
    const textbox = await screen.findByRole('textbox');
    await waitFor(() => {
      // Both runs land in the DOM. The exact tag wrapping is owned by
      // Lexical; here we just confirm the text survived.
      expect(textbox.textContent).toContain('bold');
      expect(textbox.textContent).toContain('and plain');
    });
  });

  it('renders the placeholder span when value is empty', async () => {
    const { container } = render(
      <RichText value="" placeholder="Write something" />,
    );
    await screen.findByRole('textbox');
    const placeholder = container.querySelector(
      '[data-rich-text-placeholder]',
    );
    expect(placeholder).not.toBeNull();
    expect(placeholder?.textContent).toBe('Write something');
  });

  it('does not throw when onChange is omitted', async () => {
    render(<RichText value="x" />);
    const textbox = await screen.findByRole('textbox');
    expect(textbox).toBeInTheDocument();
  });

  it('mounts cleanly with an onChange handler attached', async () => {
    // OnChangePlugin fires on user-driven edits (and once when
    // `ignoreSelectionChange` flips to false), but not synchronously
    // at mount. We just check the editor mounts without throwing —
    // actual edit-driven callbacks are exercised in browser-level
    // Playwright tests.
    const onChange = vi.fn();
    render(<RichText value="seed" onChange={onChange} />);
    const textbox = await screen.findByRole('textbox');
    expect(textbox).toBeInTheDocument();
    // No assertion on onChange call count — the contract is "if it
    // fires, signature is (html, runs)", and that's covered by the
    // type system + extractRunsFromEditor unit-tested elsewhere.
  });
});
