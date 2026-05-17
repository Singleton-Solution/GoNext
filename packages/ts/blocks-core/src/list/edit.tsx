/**
 * `core/list` Edit component.
 *
 * Lists model their items as an array of strings on disk. When the
 * block is *selected*, each item becomes its own `<RichText/>` surface
 * so authors get inline-formatting and paste support per-item. Enter
 * inside a single item is a soft break; the proper "Enter creates a
 * new list item" behaviour will land alongside the canvas split wiring
 * (see `@gonext/blocks-rich-text` `onSplit` prop). For now we accept
 * the soft-break fallback — the editor still ships a complete authoring
 * loop.
 *
 * When the block is *not* selected we render the inert `<ul>`/`<ol>`
 * directly so the canvas preview lines up with the byte-stable SSR
 * output of `save()`.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import { RichText } from '@gonext/blocks-rich-text';
import { createElement } from 'react';
import type { ListAttributes } from './save.ts';

export function ListEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<ListAttributes>): React.ReactElement {
  const tag = attributes.ordered ? 'ol' : 'ul';
  const className = [
    'gn-block-list',
    attributes.ordered ? 'gn-block-list--ordered' : 'gn-block-list--unordered',
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  // Fall back to a single placeholder item so the canvas doesn't render an
  // empty container — authors find empty list blocks visually invisible.
  const rawValues =
    attributes.values && attributes.values.length > 0
      ? attributes.values
      : [''];

  if (isSelected) {
    // When selected, each item is its own RichText surface. Updates fire
    // through a small helper that swaps the right index of the
    // `values` array without disturbing siblings.
    const updateItem = (idx: number, html: string): void => {
      const next = [...rawValues];
      next[idx] = html;
      setAttributes({ values: next });
    };
    const items = rawValues.map((v, idx) =>
      createElement(
        'li',
        { key: idx },
        <RichText
          value={v}
          onChange={(html) => updateItem(idx, html)}
          placeholder="List item"
          ariaLabel={`List item ${idx + 1}`}
        />,
      ),
    );
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

  const items = rawValues.map((v, idx) =>
    createElement('li', { key: idx }, v || 'List item'),
  );

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
