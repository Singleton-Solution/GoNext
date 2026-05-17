/**
 * `core/group` Edit component.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import { createElement } from 'react';
import type { GroupAttributes } from './save.ts';

export function GroupEdit({
  attributes,
  isSelected,
}: BlockEditProps<GroupAttributes>): React.ReactElement {
  const tag = attributes.tagName ?? 'div';
  const className = [
    'gn-block-group',
    `is-layout-${attributes.layout ?? 'default'}`,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  return createElement(tag, {
    className,
    'data-block': 'core/group',
  });
}

export default GroupEdit;
