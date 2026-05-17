/**
 * `core/quote` Edit component.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { QuoteAttributes } from './save.ts';

export function QuoteEdit({
  attributes,
  isSelected,
}: BlockEditProps<QuoteAttributes>): ReactElement {
  const className = [
    'gn-block-quote',
    attributes.style ? `is-style-${attributes.style}` : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  return (
    <blockquote className={className} data-block="core/quote">
      <p>{attributes.value || 'Quote placeholder'}</p>
      {attributes.citation ? <cite>{attributes.citation}</cite> : null}
    </blockquote>
  );
}

export default QuoteEdit;
