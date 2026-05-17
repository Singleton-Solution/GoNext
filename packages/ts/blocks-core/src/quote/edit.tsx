/**
 * `core/quote` Edit component.
 *
 * The body of the quote becomes a `<RichText/>` surface when the block
 * is selected so authors get the same inline-formatting + paste
 * affordances they have in paragraphs and headings. The citation is a
 * single line of plain text and stays a contenteditable `<cite>` for
 * now — once inline runs land in the schema we'll promote it too.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import { RichText } from '@gonext/blocks-rich-text';
import type { ReactElement } from 'react';
import type { QuoteAttributes } from './save.ts';

export function QuoteEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<QuoteAttributes>): ReactElement {
  const className = [
    'gn-block-quote',
    attributes.style ? `is-style-${attributes.style}` : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  if (isSelected) {
    return (
      <blockquote className={className} data-block="core/quote">
        <RichText
          value={attributes.value}
          onChange={(html) => setAttributes({ value: html })}
          placeholder="Quote placeholder"
          ariaLabel="Quote body"
        />
        {attributes.citation ? <cite>{attributes.citation}</cite> : null}
      </blockquote>
    );
  }

  return (
    <blockquote className={className} data-block="core/quote">
      <p>{attributes.value || 'Quote placeholder'}</p>
      {attributes.citation ? <cite>{attributes.citation}</cite> : null}
    </blockquote>
  );
}

export default QuoteEdit;
