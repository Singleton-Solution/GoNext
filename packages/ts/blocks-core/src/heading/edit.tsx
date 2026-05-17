/**
 * `core/heading` Edit component.
 *
 * Renders an `<hN>` matching the `level` attribute. The tag is computed at
 * render time so toggling level in the inspector switches the DOM element
 * without remounting the rest of the canvas.
 *
 * When the block is selected we hand authoring off to `<RichText/>`
 * (Lexical-backed, full inline-formatting + paste support); when it's
 * not selected we render the inert `<hN>` directly so the canvas
 * preview matches the byte-for-byte SSR output.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import { RichText } from '@gonext/blocks-rich-text';
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

  if (isSelected) {
    // Wrap RichText in the heading-level container so heading-specific
    // CSS still targets `.gn-block-heading--level-N`. The wrapper carries
    // `data-block` and the tag-level attributes; RichText owns the
    // editable surface inside.
    return createElement(
      tag,
      {
        className,
        'data-block': 'core/heading',
        id: attributes.anchor,
      },
      <RichText
        value={attributes.content}
        onChange={(html) => setAttributes({ content: html })}
        placeholder={`Heading H${level}`}
        ariaLabel={`Heading level ${level}`}
      />,
    );
  }

  return createElement(
    tag,
    {
      className,
      'data-block': 'core/heading',
      id: attributes.anchor,
    },
    attributes.content || `Heading H${level}`,
  );
}

export default HeadingEdit;
