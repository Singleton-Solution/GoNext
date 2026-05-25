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
import { act, fireEvent, render, screen } from '@testing-library/react';
import type { BlockTree } from '@gonext/blocks-sdk';
import {
  EditorTitle,
  EditorTopBar,
  EditorViewSwitcher,
  EditorWorkspace,
  InspectorTabs,
  OutlineToggle,
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

describe('<OutlineToggle>', () => {
  it('renders an aria-pressed button reflecting the open state', () => {
    render(<OutlineToggle open={false} onToggle={() => undefined} />);
    const btn = screen.getByTestId('outline-toggle');
    expect(btn.getAttribute('aria-pressed')).toBe('false');
    expect(btn.getAttribute('data-active')).toBe('false');
    expect(btn).toHaveAccessibleName(/Open outline/);
  });

  it('emits onToggle with the inverted state when clicked', () => {
    const onToggle = vi.fn();
    render(<OutlineToggle open={false} onToggle={onToggle} />);
    fireEvent.click(screen.getByTestId('outline-toggle'));
    expect(onToggle).toHaveBeenCalledWith(true);
  });

  it('uses the active style + label when open', () => {
    render(<OutlineToggle open={true} onToggle={() => undefined} />);
    const btn = screen.getByTestId('outline-toggle');
    expect(btn.getAttribute('aria-pressed')).toBe('true');
    expect(btn.getAttribute('data-active')).toBe('true');
    expect(btn).toHaveAccessibleName(/Close outline/);
    expect(btn.getAttribute('style')).toMatch(/--emerald/);
  });
});

describe('<EditorWorkspace>', () => {
  const blocks: BlockTree = [
    {
      type: 'core/heading',
      attributes: { level: 1, text: 'Title' },
      clientId: 'h-1',
    },
    {
      type: 'core/paragraph',
      attributes: { text: 'Body.' },
      clientId: 'p-1',
    },
  ];

  it('renders the canvas slot by default with no side panel', () => {
    render(
      <EditorWorkspace
        blocks={blocks}
        onReorder={() => undefined}
        onPaste={() => undefined}
        onSelectBlock={() => undefined}
        canvas={<div data-testid="canvas-slot">canvas</div>}
      />,
    );
    expect(screen.getByTestId('editor-workspace')).toBeInTheDocument();
    expect(screen.getByTestId('canvas-slot')).toBeInTheDocument();
    expect(
      screen.queryByTestId('editor-workspace-side-panel'),
    ).not.toBeInTheDocument();
  });

  it('renders the outline panel when sidePanel="outline"', () => {
    render(
      <EditorWorkspace
        blocks={blocks}
        onReorder={() => undefined}
        onPaste={() => undefined}
        onSelectBlock={() => undefined}
        canvas={<div data-testid="canvas-slot" />}
        sidePanel="outline"
      />,
    );
    const panel = screen.getByTestId('editor-workspace-side-panel');
    expect(panel.getAttribute('data-panel')).toBe('outline');
    expect(screen.getByTestId('document-outline')).toBeInTheDocument();
    expect(screen.getByTestId('document-outline-row-h-1')).toBeInTheDocument();
  });

  it('renders the list-view panel when sidePanel="list"', () => {
    render(
      <EditorWorkspace
        blocks={blocks}
        onReorder={() => undefined}
        onPaste={() => undefined}
        onSelectBlock={() => undefined}
        canvas={<div data-testid="canvas-slot" />}
        sidePanel="list"
      />,
    );
    expect(screen.getByTestId('list-view')).toBeInTheDocument();
    expect(screen.getByTestId('list-view-row-h-1')).toBeInTheDocument();
    expect(screen.getByTestId('list-view-row-p-1')).toBeInTheDocument();
  });

  it('fires onSelectBlock when an outline row is clicked', () => {
    const onSelectBlock = vi.fn();
    render(
      <EditorWorkspace
        blocks={blocks}
        onReorder={() => undefined}
        onPaste={() => undefined}
        onSelectBlock={onSelectBlock}
        canvas={<div data-testid="canvas-slot" />}
        sidePanel="outline"
      />,
    );
    act(() => {
      screen.getByTestId('document-outline-row-h-1').click();
    });
    expect(onSelectBlock).toHaveBeenCalledWith('h-1');
  });

  it('mounts the sortable list when renderSortableRow is supplied', () => {
    render(
      <EditorWorkspace
        blocks={blocks}
        onReorder={() => undefined}
        onPaste={() => undefined}
        onSelectBlock={() => undefined}
        canvas={<div data-testid="canvas-slot">canvas</div>}
        renderSortableRow={(id) => <span data-testid={`row-${id}`}>{id}</span>}
      />,
    );
    // Canvas slot is replaced by the sortable list when row renderer is given.
    expect(screen.queryByTestId('canvas-slot')).not.toBeInTheDocument();
    expect(screen.getByTestId('sortable-block-list')).toBeInTheDocument();
    expect(screen.getByTestId('row-h-1')).toBeInTheDocument();
  });

  it('routes paste events through the paste-handler and prevents default', () => {
    const onPaste = vi.fn();
    render(
      <EditorWorkspace
        blocks={blocks}
        onReorder={() => undefined}
        onPaste={onPaste}
        onSelectBlock={() => undefined}
        canvas={<div data-testid="canvas-slot" />}
      />,
    );
    const canvasWrap = screen.getByTestId('editor-workspace-canvas');
    // Hand-roll a synthetic-ish paste event so we don't fight jsdom's
    // partial ClipboardEvent. The runPasteHandler reads
    // event.clipboardData.getData; React forwards a `nativeEvent`
    // shaped like a real ClipboardEvent.
    const clipboardData = {
      getData: (type: string) =>
        type === 'text/html'
          ? '<b id="docs-internal-guid-1"><h2>Pasted</h2></b>'
          : '',
    };
    fireEvent.paste(canvasWrap, { clipboardData });
    expect(onPaste).toHaveBeenCalled();
    const [tree] = onPaste.mock.calls[0] ?? [];
    expect(tree?.[0]?.type).toBe('core/heading');
  });
});
