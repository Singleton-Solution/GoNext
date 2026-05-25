/**
 * `<DocTitleEditor>` — the headline-style document title input at
 * the top of the editor canvas.
 *
 * The brand mock (`docs/design/ui_kits/editor/index.html`) renders
 * the title as a 56px Archivo 800 headline with one italic
 * "Instrument Serif" accent word. Placeholder copy is italic too —
 * "Untitled *draft*." — which is the brand's signature move.
 *
 * The component is a controlled `<textarea>`. We use textarea (not
 * input) because the design lets the title wrap to two lines and a
 * single-line input would clip. The autogrow trick is the standard
 * CSS-grid one (the field shares its parent with a hidden sizer);
 * we keep the markup simple and let the host layout decide.
 *
 * The placeholder slot is rendered as a sibling node so the italic
 * accent can pick up Instrument Serif while the input itself stays
 * in Archivo. Real input chrome (focus ring, etc.) lives in
 * `editor-theme.css`.
 */
'use client';

import { useCallback } from 'react';

export interface DocTitleEditorProps {
  /** The current title text. */
  value: string;
  /** Called on every keystroke with the new value. */
  onChange: (next: string) => void;
  /**
   * Placeholder copy rendered when `value` is empty. Defaults to
   * `'Untitled *draft*.'` — the spec wants this italic-accent
   * pattern. Use `*word*` to opt in to the Instrument Serif italic
   * swap for that word.
   */
  placeholder?: string;
  /** Optional ID for label association. */
  id?: string;
  /**
   * Optional aria-label. Defaults to `'Document title'` so screen
   * readers don't read out the italic-accent placeholder text.
   */
  ariaLabel?: string;
}

/**
 * Parse a `*word*` italic-accent placeholder into spans the CSS can
 * style. Exported for testing.
 */
export function parsePlaceholder(text: string): Array<{
  kind: 'plain' | 'accent';
  text: string;
}> {
  const parts: Array<{ kind: 'plain' | 'accent'; text: string }> = [];
  const re = /\*([^*]+)\*/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) {
      parts.push({ kind: 'plain', text: text.slice(last, m.index) });
    }
    parts.push({ kind: 'accent', text: m[1] ?? '' });
    last = m.index + m[0].length;
  }
  if (last < text.length) {
    parts.push({ kind: 'plain', text: text.slice(last) });
  }
  if (parts.length === 0) parts.push({ kind: 'plain', text });
  return parts;
}

/**
 * The headline-style document title editor.
 */
export function DocTitleEditor({
  value,
  onChange,
  placeholder = 'Untitled *draft*.',
  id,
  ariaLabel = 'Document title',
}: DocTitleEditorProps) {
  const handleChange = useCallback(
    (event: React.ChangeEvent<HTMLTextAreaElement>) => {
      onChange(event.target.value);
    },
    [onChange],
  );

  const placeholderParts = parsePlaceholder(placeholder);
  const isEmpty = value.length === 0;

  return (
    <div
      className="gonext-doc-title-editor"
      data-testid="doc-title-editor"
    >
      <textarea
        id={id}
        className="gonext-doc-title-editor__field"
        data-testid="doc-title-editor-field"
        value={value}
        onChange={handleChange}
        aria-label={ariaLabel}
        rows={1}
        spellCheck={true}
      />
      {isEmpty ? (
        <span
          aria-hidden="true"
          className="gonext-doc-title-editor__placeholder-hint"
          data-testid="doc-title-editor-placeholder-hint"
        >
          {placeholderParts.map((part, i) =>
            part.kind === 'accent' ? (
              <em key={i} data-testid="doc-title-editor-placeholder-accent">
                {part.text}
              </em>
            ) : (
              <span key={i}>{part.text}</span>
            ),
          )}
        </span>
      ) : null}
    </div>
  );
}
