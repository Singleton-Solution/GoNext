/**
 * `core/columns` Edit component.
 *
 * Container block. Children are walked by `<BlockEditCanvas>` itself — this
 * component just renders the outer wrapper so the canvas's recursion has
 * something visible to attach children to.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { ColumnsAttributes } from './save.ts';

export function ColumnsEdit({
  attributes,
  isSelected,
}: BlockEditProps<ColumnsAttributes>): ReactElement {
  const className = [
    'gn-block-columns',
    `gn-block-columns--cols-${attributes.columns}`,
    attributes.isStackedOnMobile !== false ? 'is-stacked-on-mobile' : null,
    attributes.verticalAlignment
      ? `is-vertically-aligned-${attributes.verticalAlignment}`
      : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  return <div className={className} data-block="core/columns" />;
}

export default ColumnsEdit;
