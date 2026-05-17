/**
 * `core/heading` Edit component.
 *
 * Renders an `<hN>` matching the `level` attribute. The tag is computed at
 * render time so toggling level in the inspector switches the DOM element
 * without remounting the rest of the canvas.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import { createElement } from 'react';
import type { HeadingAttributes } from './save.ts';

export function HeadingEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<HeadingAttributes>): React.ReactElement {
  const level = attributes.level ?? 2;
  const tag = `h${level}`;

  const className = [
    'gn-block-heading',
    `gn-block-heading--level-${level}`,
    attributes.align ? `has-text-align-${attributes.align}` : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  return createElement(
    tag,
    {
      className,
      'data-block': 'core/heading',
      id: attributes.anchor,
      contentEditable: isSelected,
      suppressContentEditableWarning: true,
      onInput: (event: React.FormEvent<HTMLElement>) =>
        setAttributes({
          content: (event.currentTarget as HTMLElement).textContent ?? '',
        }),
    },
    attributes.content || `Heading H${level}`,
  );
}

export default HeadingEdit;
