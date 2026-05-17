/**
 * `core/paragraph` Edit component.
 *
 * When the block is *selected*, the editor mounts the canonical
 * `<RichText/>` primitive from `@gonext/blocks-rich-text` — a Lexical-
 * powered surface with proper IME, undo/redo, paste handling, inline
 * formatting, and link insertion. When the block is *not* selected, we
 * fall back to a plain non-editable `<p>` rendered with the persisted
 * `content` string — this matches what `save()` would emit so the
 * canvas preview is visually identical to the rendered output.
 *
 * The selected/unselected fork is deliberate: only the focused block
 * needs the editor weight, and rendering the inert preview as plain
 * DOM keeps the canvas snappy even with hundreds of blocks visible.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import { RichText } from '@gonext/blocks-rich-text';
import type { ReactElement } from 'react';
import type { ParagraphAttributes } from './save.ts';

export function ParagraphEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<ParagraphAttributes>): ReactElement {
  // Build the same class list as the save() output so authoring + published
  // markup share styles. Theme designers can target `.gn-block-paragraph`
  // once and have it apply everywhere.
  const classes = [
    'gn-block-paragraph',
    attributes.align ? `has-text-align-${attributes.align}` : null,
    attributes.dropCap ? 'has-drop-cap' : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  if (isSelected) {
    return (
      <RichText
        value={attributes.content}
        onChange={(html) => setAttributes({ content: html })}
        placeholder="Paragraph placeholder"
        className={classes}
        dataBlock="core/paragraph"
        ariaLabel="Paragraph content"
      />
    );
  }

  return (
    <p className={classes} data-block="core/paragraph">
      {attributes.content || 'Paragraph placeholder'}
    </p>
  );
}

export default ParagraphEdit;
