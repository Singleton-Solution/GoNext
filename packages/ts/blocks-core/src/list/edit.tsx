/**
 * `core/list` Edit component.
 *
 * The current shape is a read-mostly preview of the list. Authoring per-item
 * edits arrives with the rich-text milestone; until then, item content is
 * driven through the inspector or programmatic `setAttributes` calls.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import { createElement } from 'react';
import type { ListAttributes } from './save.ts';

export function ListEdit({
  attributes,
  isSelected,
}: BlockEditProps<ListAttributes>): React.ReactElement {
  const tag = attributes.ordered ? 'ol' : 'ul';
  const items = (attributes.values ?? []).map((v, idx) =>
    createElement('li', { key: idx }, v || 'List item'),
  );
  // Fall back to a single placeholder item so the canvas doesn't render an
  // empty container — authors find empty list blocks visually invisible.
  if (items.length === 0) {
    items.push(createElement('li', { key: 'placeholder' }, 'List item'));
  }

  const className = [
    'gn-block-list',
    attributes.ordered ? 'gn-block-list--ordered' : 'gn-block-list--unordered',
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  return createElement(
    tag,
    {
      className,
      'data-block': 'core/list',
      start: attributes.ordered ? attributes.start : undefined,
      reversed: attributes.ordered ? attributes.reversed : undefined,
    },
    items,
  );
}

export default ListEdit;
