/**
 * `core/paragraph` Edit component.
 *
 * Minimal contenteditable surface: a single-line `<p contenteditable>` that
 * mirrors the persisted `content` attribute back through `setAttributes`.
 * The component is deliberately small — rich-text inline runs (bold, links,
 * etc.) live in a follow-up issue. What's here is enough for the inserter
 * + canvas walk-through and to satisfy the round-trip tests.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
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

  return (
    <p
      className={classes}
      data-block="core/paragraph"
      contentEditable={isSelected}
      suppressContentEditableWarning
      onInput={(event) =>
        setAttributes({
          content: (event.target as HTMLParagraphElement).textContent ?? '',
        })
      }
    >
      {attributes.content || 'Paragraph placeholder'}
    </p>
  );
}

export default ParagraphEdit;
