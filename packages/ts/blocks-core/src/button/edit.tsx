/**
 * `core/button` Edit component.
 *
 * Buttons are tiny; we mount a simple `contenteditable` span over the
 * persisted `text` and commit the new value on blur. The URL / target /
 * style controls live in the right-hand inspector pane — this component
 * just hosts the canvas representation.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { CSSProperties, FocusEvent, ReactElement } from 'react';
import type { ButtonAttributes } from './save.ts';

export function ButtonEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<ButtonAttributes>): ReactElement {
  const wrapperClass = [
    'wp-block-button',
    attributes.style ? `is-style-${attributes.style}` : null,
    attributes.align ? `has-text-align-${attributes.align}` : null,
    isSelected ? 'is-selected' : null,
  ]
    .filter((c): c is string => c !== null)
    .join(' ');

  const style: CSSProperties | undefined =
    attributes.borderRadius !== undefined
      ? { borderRadius: `${attributes.borderRadius}px` }
      : undefined;

  // Commit text on blur — we let the DOM hold the in-progress value and
  // only push it back through React when focus leaves.
  const handleBlur = (event: FocusEvent<HTMLAnchorElement>): void => {
    const next = event.currentTarget.textContent ?? '';
    if (next !== attributes.text) {
      setAttributes({ text: next });
    }
  };

  return (
    <div className={wrapperClass} data-block="core/button">
      <a
        className="wp-block-button__link"
        href={attributes.url}
        target={attributes.linkTarget}
        rel={attributes.linkTarget === '_blank' ? 'noopener noreferrer' : undefined}
        style={style}
        contentEditable={isSelected}
        suppressContentEditableWarning
        onBlur={handleBlur}
      >
        {attributes.text || 'Add text…'}
      </a>
    </div>
  );
}

export default ButtonEdit;
