/**
 * `core/separator` Edit component.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { SeparatorAttributes } from './save.ts';

export function SeparatorEdit({
  attributes,
  isSelected,
}: BlockEditProps<SeparatorAttributes>): ReactElement {
  const className = [
    'gn-block-separator',
    `is-style-${attributes.style ?? 'default'}`,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  return <hr className={className} data-block="core/separator" />;
}

export default SeparatorEdit;
