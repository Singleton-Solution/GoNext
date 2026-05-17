/**
 * `core/spacer` Edit component.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { SpacerAttributes } from './save.ts';

export function SpacerEdit({
  attributes,
  isSelected,
}: BlockEditProps<SpacerAttributes>): ReactElement {
  const className = [
    'gn-block-spacer',
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  return (
    <div
      className={className}
      data-block="core/spacer"
      aria-hidden="true"
      style={{ height: `${attributes.height}px` }}
    />
  );
}

export default SpacerEdit;
