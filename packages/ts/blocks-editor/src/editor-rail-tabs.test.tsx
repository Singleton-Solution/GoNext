/**
 * Tests for `<EditorRailTabs>` — the right-rail inspector tab strip.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { EditorRailTabs } from './editor-rail-tabs.tsx';

const tabs = [
  { id: 'block', label: 'Block' },
  { id: 'document', label: 'Document' },
  { id: 'seo', label: 'SEO' },
] as const;

describe('<EditorRailTabs>', () => {
  it('renders one tab per entry with stable test ids', () => {
    render(<EditorRailTabs tabs={tabs} />);
    expect(screen.getByTestId('editor-rail-tabs-tab-block')).toHaveTextContent(
      'Block',
    );
    expect(
      screen.getByTestId('editor-rail-tabs-tab-document'),
    ).toHaveTextContent('Document');
    expect(screen.getByTestId('editor-rail-tabs-tab-seo')).toHaveTextContent(
      'SEO',
    );
  });

  it('defaults the active tab to the first entry', () => {
    render(<EditorRailTabs tabs={tabs} />);
    expect(
      screen.getByTestId('editor-rail-tabs-tab-block').getAttribute('data-active'),
    ).toBe('true');
    expect(
      screen
        .getByTestId('editor-rail-tabs-tab-document')
        .getAttribute('data-active'),
    ).toBe('false');
  });

  it('honors defaultValue when uncontrolled', () => {
    render(<EditorRailTabs tabs={tabs} defaultValue="seo" />);
    expect(
      screen.getByTestId('editor-rail-tabs-tab-seo').getAttribute('data-active'),
    ).toBe('true');
  });

  it('fires onChange and flips data-active when uncontrolled', () => {
    const onChange = vi.fn();
    render(<EditorRailTabs tabs={tabs} onChange={onChange} />);

    fireEvent.click(screen.getByTestId('editor-rail-tabs-tab-document'));
    expect(onChange).toHaveBeenCalledWith('document');
    expect(
      screen
        .getByTestId('editor-rail-tabs-tab-document')
        .getAttribute('data-active'),
    ).toBe('true');
    expect(
      screen.getByTestId('editor-rail-tabs-tab-block').getAttribute('data-active'),
    ).toBe('false');
  });

  it('stays controlled when value is provided (no internal state)', () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <EditorRailTabs tabs={tabs} value="block" onChange={onChange} />,
    );

    fireEvent.click(screen.getByTestId('editor-rail-tabs-tab-document'));
    expect(onChange).toHaveBeenCalledWith('document');
    // No re-render with a new value — should still highlight block.
    expect(
      screen.getByTestId('editor-rail-tabs-tab-block').getAttribute('data-active'),
    ).toBe('true');

    rerender(<EditorRailTabs tabs={tabs} value="document" onChange={onChange} />);
    expect(
      screen
        .getByTestId('editor-rail-tabs-tab-document')
        .getAttribute('data-active'),
    ).toBe('true');
  });

  it('exposes the BEM hooks the editor-theme stylesheet reads', () => {
    render(<EditorRailTabs tabs={tabs} />);
    const root = screen.getByTestId('editor-rail-tabs');
    expect(root.className).toContain('gonext-editor-rail-tabs');
    const blockTab = screen.getByTestId('editor-rail-tabs-tab-block');
    expect(blockTab.className).toBe('gonext-editor-rail-tabs__tab');
  });
});
