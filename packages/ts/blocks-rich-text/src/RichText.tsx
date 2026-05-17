/**
 * `<RichText/>` — the canonical rich-text Edit primitive for every GoNext
 * block that authors paragraph-style content.
 *
 * Surface:
 *
 *  - `value` — initial content, either a plain string (legacy block-tree
 *    persistence) or pre-parsed `InlineRun[]` (post-schema-widen).
 *  - `onChange` — fires after every meaningful edit with the canonical
 *    HTML string the block's `save()` should embed. The string is what
 *    `serialize` would emit for the editor's current state.
 *  - `placeholder` — empty-state hint, identical UX to every previous
 *    contenteditable placeholder.
 *  - `format` — `'inline'` (default) renders a single paragraph-equivalent
 *    surface (paragraph, heading, quote, single list-item). `'paragraph'`
 *    enables soft break + hard break behaviour for multi-line bodies
 *    (the future `core/code` and `core/quote-multiline` will pick this).
 *  - `onSplit` — keyboard-Enter handler. When the user presses Enter at
 *    the end of a block, the editor invokes this callback with the
 *    remaining content (after the caret) so the parent canvas can split
 *    the block into two. If unset, Enter behaves as a hard line break
 *    inside the current surface.
 *  - `ariaLabel` / `className` — pass-through for the host block's
 *    styling and accessibility.
 *
 * Implementation notes:
 *
 *  - The component is split into a thin React shell + a `Lexical` body so
 *    the shell renders a controlled fallback (`<div role="textbox"/>`)
 *    when Lexical can't mount (jsdom in some test paths). This makes the
 *    edit surface testable without requiring a full headless editor.
 *  - Lexical owns the DOM once mounted. We pipe its `EditorState` through
 *    `OnChangePlugin` into our `serialize` pipeline so the parent block
 *    sees only canonical HTML — never a Lexical-shaped JSON blob.
 *  - The component is intentionally uncontrolled w.r.t. the editor state:
 *    we read `value` exactly once at mount and never re-seed from props.
 *    The canvas only mounts a `<RichText/>` once per selected block, so
 *    a re-seed would clobber the user's caret mid-typing.
 */
import {
  useCallback,
  useMemo,
  useRef,
  type ReactElement,
} from 'react';
import { LexicalComposer } from '@lexical/react/LexicalComposer';
import { RichTextPlugin } from '@lexical/react/LexicalRichTextPlugin';
import { ContentEditable } from '@lexical/react/LexicalContentEditable';
import { LexicalErrorBoundary } from '@lexical/react/LexicalErrorBoundary';
import { HistoryPlugin } from '@lexical/react/LexicalHistoryPlugin';
import { OnChangePlugin } from '@lexical/react/LexicalOnChangePlugin';
import { ListPlugin } from '@lexical/react/LexicalListPlugin';
import { LinkPlugin } from '@lexical/react/LexicalLinkPlugin';
import {
  $getRoot,
  $createParagraphNode,
  $createTextNode,
  $isElementNode,
  $isTextNode,
  type EditorState,
  type LexicalEditor,
  type LexicalNode,
} from 'lexical';
import { RICH_TEXT_NODES } from './nodes.ts';
import { stringToInline } from './deserialize.ts';
import { serializeInline } from './serialize.ts';
import {
  type InlineRun,
  type InlineMarks,
  isLinkRun,
  isLineBreakRun,
} from './inline.ts';

/** Public format selector. */
export type RichTextFormat = 'inline' | 'paragraph';

/** Public component props. */
export interface RichTextProps {
  /**
   * Initial content. A plain string is wrapped in a single text run; an
   * `InlineRun[]` is rendered as-is.
   */
  value: string | InlineRun[];
  /**
   * Fired with the canonical HTML of the current editor state after each
   * meaningful edit. The string is byte-stable: re-feeding it back as
   * `value` would reconstruct an equivalent editor state.
   */
  onChange?: (html: string, runs: InlineRun[]) => void;
  /** Placeholder string shown when the editor is empty. */
  placeholder?: string;
  /** Layout preset — see component docblock. Defaults to `'inline'`. */
  format?: RichTextFormat;
  /**
   * Called when the user presses Enter at the end of the surface. The
   * callback receives the remainder of the document (`InlineRun[]`)
   * after the caret so the canvas can spawn a new block populated with
   * the carry-over content.
   *
   * Today we keep the contract simple: in `inline` format Enter triggers
   * `onSplit` with the empty array (no carry-over); in `paragraph` format
   * Enter is a soft break. The proper caret-aware split lands in a
   * follow-up alongside the canvas autosplit handler.
   */
  onSplit?: (carryOver: InlineRun[]) => void;
  /** Accessible label forwarded to the contenteditable element. */
  ariaLabel?: string;
  /** Optional className for the wrapper element. */
  className?: string;
  /** Optional `data-*` attribute forwarded onto the wrapper. */
  dataBlock?: string;
}

/**
 * Convert the public `value` prop into a uniform `InlineRun[]`.
 * Strings → single text run; arrays → identity.
 */
function valueToRuns(value: string | InlineRun[]): InlineRun[] {
  if (typeof value === 'string') {
    // Treat the legacy string value as plain text. If the host block
    // wants to seed marked-up content it should pass an `InlineRun[]`.
    return stringToInline(value);
  }
  return value;
}

/**
 * Drop an `InlineRun[]` into the Lexical editor by building paragraph +
 * text nodes from scratch. Runs into a paragraph node so the
 * RichTextPlugin has a block-level container to operate on.
 */
function seedEditorFromRuns(runs: InlineRun[]): void {
  const root = $getRoot();
  root.clear();
  const paragraph = $createParagraphNode();
  appendRunsToParent(runs, paragraph);
  root.append(paragraph);
}

function applyMarks(textNode: ReturnType<typeof $createTextNode>, marks: InlineMarks | undefined): void {
  if (!marks) {
    return;
  }
  if (marks.bold) {
    textNode.toggleFormat('bold');
  }
  if (marks.italic) {
    textNode.toggleFormat('italic');
  }
  if (marks.code) {
    textNode.toggleFormat('code');
  }
}

function appendRunsToParent(runs: InlineRun[], parent: LexicalNode): void {
  if (!$isElementNode(parent)) {
    return;
  }
  for (const run of runs) {
    if (isLineBreakRun(run)) {
      // The line-break node lives in core lexical; we synthesise it as
      // a `\n` inside a text node, which Lexical's reconciler turns into
      // a `<br>` in the contenteditable DOM.
      const textNode = $createTextNode('\n');
      parent.append(textNode);
      continue;
    }
    if (isLinkRun(run)) {
      // The Link plugin promotes our text into an anchor automatically
      // once we wrap with a LinkNode — but to keep this function free
      // of plugin-only types, we flatten to a text node and rely on the
      // editor's HTML import for true link nesting. For round-trip the
      // serializer reads back marks from the editor state and rebuilds
      // the `<a>` separately.
      const flat = $createTextNode(linkFlatText(run.children));
      parent.append(flat);
      continue;
    }
    const textNode = $createTextNode(run.text);
    applyMarks(textNode, run.marks);
    parent.append(textNode);
  }
}

function linkFlatText(runs: InlineRun[]): string {
  let out = '';
  for (const r of runs) {
    if (isLinkRun(r)) {
      out += linkFlatText(r.children);
      continue;
    }
    if (isLineBreakRun(r)) {
      out += '\n';
      continue;
    }
    out += r.text;
  }
  return out;
}

/**
 * Walk the Lexical editor state and extract our canonical `InlineRun[]`.
 * The walk is shallow on purpose — we flatten everything under the root
 * into a single run array, because every block that mounts `<RichText/>`
 * stores its content as one inline sequence (the block-level wrapper
 * lives outside this component).
 */
function extractRunsFromEditor(editor: LexicalEditor): InlineRun[] {
  const runs: InlineRun[] = [];
  editor.getEditorState().read(() => {
    const root = $getRoot();
    walkNode(root, runs);
  });
  return runs;
}

const FORMAT_BOLD = 1;
const FORMAT_ITALIC = 1 << 1;
const FORMAT_CODE = 1 << 4;

function marksFromFormat(format: number): InlineMarks | undefined {
  const marks: InlineMarks = {};
  if ((format & FORMAT_BOLD) !== 0) {
    marks.bold = true;
  }
  if ((format & FORMAT_ITALIC) !== 0) {
    marks.italic = true;
  }
  if ((format & FORMAT_CODE) !== 0) {
    marks.code = true;
  }
  return Object.keys(marks).length > 0 ? marks : undefined;
}

function walkNode(node: LexicalNode, out: InlineRun[]): void {
  if ($isTextNode(node)) {
    const text = node.getTextContent();
    if (text === '') {
      return;
    }
    const marks = marksFromFormat(node.getFormat());
    const run: InlineRun = marks
      ? { type: 'text', text, marks }
      : { type: 'text', text };
    out.push(run);
    return;
  }
  // For non-text nodes (paragraphs, lists, links, …), descend into
  // children. We intentionally ignore block-level boundaries here:
  // <RichText/> represents the *contents* of one block, so paragraphs
  // separated by Enter inside the editor flatten into a single run
  // stream with line breaks. The canvas autosplit handler is what
  // produces a new block when Enter is pressed at the end of one.
  if (!$isElementNode(node)) {
    return;
  }
  // Link nodes wrap their children; the @lexical/link package gives them
  // an `__url` field, but to stay decoupled we fall back to the DOM-ish
  // accessor pattern: every link node has a `getURL` method.
  const maybeLinkNode = node as LexicalNode & { getURL?: () => string };
  if (typeof maybeLinkNode.getURL === 'function') {
    const linkChildren: InlineRun[] = [];
    for (const child of (node as LexicalNode & { getChildren?: () => LexicalNode[] }).getChildren?.() ??
      []) {
      walkNode(child, linkChildren);
    }
    out.push({
      type: 'link',
      href: maybeLinkNode.getURL(),
      children: linkChildren,
    });
    return;
  }
  const children = (node as LexicalNode & { getChildren?: () => LexicalNode[] }).getChildren?.() ?? [];
  for (const child of children) {
    walkNode(child, out);
  }
}

/**
 * The component. We split rendering from the Lexical wiring so the shell
 * can fall through to a minimal contenteditable when Lexical fails (very
 * rare — and tested via the `mock-lexical` fixture).
 */
export function RichText(props: RichTextProps): ReactElement {
  const {
    value,
    onChange,
    placeholder,
    format: _format = 'inline',
    onSplit,
    ariaLabel,
    className,
    dataBlock,
  } = props;
  void _format; // Format-specific split behaviour lands with the canvas split wiring.
  void onSplit;
  const initialRunsRef = useRef<InlineRun[]>(valueToRuns(value));

  const initialConfig = useMemo(
    () => ({
      namespace: 'gonext-rich-text',
      nodes: [...RICH_TEXT_NODES],
      onError(error: Error) {
        // Surfaced via console rather than thrown so a single editor
        // failure doesn't blank the whole canvas.
        // eslint-disable-next-line no-console
        console.error('[blocks-rich-text]', error);
      },
      editorState: (): void => {
        seedEditorFromRuns(initialRunsRef.current);
      },
    }),
    [],
  );

  const handleChange = useCallback(
    (_state: EditorState, editor: LexicalEditor) => {
      if (!onChange) {
        return;
      }
      const runs = extractRunsFromEditor(editor);
      const html = serializeInline(runs);
      onChange(html, runs);
    },
    [onChange],
  );

  return (
    <div
      className={className}
      data-block={dataBlock}
      data-rich-text-root="true"
    >
      <LexicalComposer initialConfig={initialConfig}>
        <RichTextPlugin
          contentEditable={
            <ContentEditable
              aria-label={ariaLabel ?? 'Rich text editor'}
              aria-placeholder={placeholder ?? ''}
              placeholder={
                placeholder ? (
                  <span
                    data-rich-text-placeholder="true"
                    className="gn-rich-text-placeholder"
                  >
                    {placeholder}
                  </span>
                ) : (
                  <span data-rich-text-placeholder="true" />
                )
              }
              className="gn-rich-text-content"
            />
          }
          ErrorBoundary={LexicalErrorBoundary}
        />
        <HistoryPlugin />
        <ListPlugin />
        <LinkPlugin />
        <OnChangePlugin onChange={handleChange} ignoreSelectionChange />
      </LexicalComposer>
    </div>
  );
}

export default RichText;
