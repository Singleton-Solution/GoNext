/**
 * Tests for editor-chrome surfaces.
 *
 * The components are visual scaffolds — they don't own block state
 * and don't talk to Lexical. The contract we cover:
 *
 *   1. `<EditorTopBar>` renders the forest strip with crumb / view-
 *      switcher / actions slots.
 *   2. `<EditorViewSwitcher>` highlights the active view with the
 *      forest-3 pill (the mock's `.topbar .center button.on`).
 *   3. `<EditorTitle>` renders the contenteditable surface, fires
 *      `onChange` with the new text, and shows a brand-styled
 *      placeholder with an Instrument Serif italic accent.
 *   4. `<InspectorTabs>` highlights the active tab with a lavender
 *      bottom-border (the mock's secondary-accent inspector).
 *   5. `<UncontrolledInspectorTabs>` keeps its own state and surfaces
 *      changes via the `onChange` callback.
 *   6. Snapshots for the top bar, title with placeholder, and
 *      inspector tabs.
 */
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import {
  EditorTitle,
  EditorTopBar,
  EditorViewSwitcher,
  InspectorTabs,
  UncontrolledInspectorTabs,
} from './editor-chrome.tsx';

describe('<EditorTopBar>', () => {
  it('renders the three slots and the forest surface', () => {
    render(
      <EditorTopBar
        crumb={<span data-testid="crumb">Posts</span>}
        viewSwitcher={<span data-testid="vsw">Write</span>}
        actions={<span data-testid="acts">Publish</span>}
      />,
    );

    expect(screen.getByTestId('crumb')).toBeInTheDocument();
    expect(screen.getByTestId('vsw')).toBeInTheDocument();
    expect(screen.getByTestId('acts')).toBeInTheDocument();
    const bar = screen.getByTestId('editor-top-bar');
    expect(bar.getAttribute('style')).toMatch(/--forest/);
    expect(bar.getAttribute('data-surface')).toBe('forest');
  });

  it('matches the snapshot for the canonical layout', () => {
    const { container } = render(
      <EditorTopBar
        crumb={<span>Posts</span>}
        viewSwitcher={<span>Write</span>}
        actions={<span>Publish</span>}
      />,
    );
    expect(container.firstChild).toMatchSnapshot();
  });
});

describe('<EditorViewSwitcher>', () => {
  it('highlights the active view and fires onChange when another is clicked', () => {
    const onChange = vi.fn();
    render(
      <EditorViewSwitcher
        views={[
          { id: 'write', label: 'Write' },
          { id: 'preview', label: 'Preview' },
          { id: 'diff', label: 'Diff' },
        ]}
        activeId="write"
        onChange={onChange}
      />,
    );

    expect(
      screen.getByTestId('editor-view-switcher-write').getAttribute('data-active'),
    ).toBe('true');
    fireEvent.click(screen.getByTestId('editor-view-switcher-preview'));
    expect(onChange).toHaveBeenCalledWith('preview');
  });
});

describe('<EditorTitle>', () => {
  it('shows the italic-accent placeholder when value is empty', () => {
    render(<EditorTitle value="" placeholder="Sites that *live*." />);

    const ph = screen.getByTestId('editor-title-placeholder');
    expect(ph).toBeInTheDocument();
    const accent = screen.getByTestId('editor-title-placeholder-accent');
    expect(accent).toHaveTextContent('live');
    expect(accent.getAttribute('style')).toMatch(/--font-serif/);
    expect(accent.getAttribute('style')).toMatch(/--emerald-deep/);
  });

  it('omits the placeholder once the title has content', () => {
    render(<EditorTitle value="Single-origin." />);
    expect(screen.queryByTestId('editor-title-placeholder')).toBeNull();
    expect(screen.getByTestId('editor-title')).toHaveTextContent(
      'Single-origin.',
    );
  });

  it('fires onChange with the new text content on input', () => {
    const onChange = vi.fn();
    render(<EditorTitle value="" onChange={onChange} />);

    const title = screen.getByTestId('editor-title');
    title.textContent = 'Hello.';
    fireEvent.input(title);
    expect(onChange).toHaveBeenCalledWith('Hello.');
  });

  it('uses the Archivo display family and -0.03em tracking', () => {
    render(<EditorTitle value="x" />);
    const title = screen.getByTestId('editor-title');
    expect(title.getAttribute('style')).toMatch(/--font-display/);
    expect(title.getAttribute('style')).toMatch(/letter-spacing: -0\.03em/);
  });

  it('matches the snapshot for an empty title with default placeholder', () => {
    const { container } = render(<EditorTitle value="" />);
    expect(container.firstChild).toMatchSnapshot();
  });
});

describe('<InspectorTabs>', () => {
  it('renders the tabs and marks the active one with lavender', () => {
    const onChange = vi.fn();
    render(
      <InspectorTabs
        tabs={[
          { id: 'block', label: 'Block' },
          { id: 'document', label: 'Document' },
          { id: 'seo', label: 'SEO' },
        ]}
        activeId="block"
        onChange={onChange}
      />,
    );

    const active = screen.getByTestId('inspector-tab-block');
    expect(active.getAttribute('data-active')).toBe('true');
    expect(active.getAttribute('style')).toMatch(/--lavender/);

    fireEvent.click(screen.getByTestId('inspector-tab-document'));
    expect(onChange).toHaveBeenCalledWith('document');
  });

  it('matches the snapshot for the default three-tab layout', () => {
    const { container } = render(
      <InspectorTabs
        tabs={[
          { id: 'block', label: 'Block' },
          { id: 'document', label: 'Document' },
          { id: 'seo', label: 'SEO' },
        ]}
        activeId="block"
        onChange={vi.fn()}
      />,
    );
    expect(container.firstChild).toMatchSnapshot();
  });
});

describe('<UncontrolledInspectorTabs>', () => {
  it('keeps its own state and surfaces changes via onChange', () => {
    const onChange = vi.fn();
    render(
      <UncontrolledInspectorTabs
        tabs={[
          { id: 'block', label: 'Block' },
          { id: 'document', label: 'Document' },
        ]}
        onChange={onChange}
      />,
    );

    expect(
      screen.getByTestId('inspector-tab-block').getAttribute('data-active'),
    ).toBe('true');
    fireEvent.click(screen.getByTestId('inspector-tab-document'));
    expect(onChange).toHaveBeenCalledWith('document');
    expect(
      screen.getByTestId('inspector-tab-document').getAttribute('data-active'),
    ).toBe('true');
  });

  it('respects defaultId when provided', () => {
    render(
      <UncontrolledInspectorTabs
        tabs={[
          { id: 'block', label: 'Block' },
          { id: 'document', label: 'Document' },
        ]}
        defaultId="document"
      />,
    );
    expect(
      screen.getByTestId('inspector-tab-document').getAttribute('data-active'),
    ).toBe('true');
  });
});
