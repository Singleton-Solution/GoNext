/**
 * `core/file` Edit component.
 *
 * The canvas representation mirrors the `save()` output: a text link
 * (or span, when `textLinkHref` is false) plus an optional download
 * button. Editing the file name happens inline via `contenteditable`;
 * URL / toggle controls live in the inspector.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { FocusEvent, ReactElement } from 'react';
import type { FileAttributes } from './save.ts';

export function FileEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<FileAttributes>): ReactElement {
  const className = ['wp-block-file', isSelected ? 'is-selected' : null]
    .filter((c): c is string => c !== null)
    .join(' ');

  const showLink = attributes.textLinkHref !== false;
  const showButton = attributes.downloadButton !== false;

  const handleBlur = (
    event: FocusEvent<HTMLAnchorElement | HTMLSpanElement>,
  ): void => {
    const next = event.currentTarget.textContent ?? '';
    if (next !== attributes.fileName) {
      setAttributes({ fileName: next });
    }
  };

  const name = attributes.fileName || 'untitled.pdf';
  const textNode = showLink ? (
    <a
      href={attributes.href}
      contentEditable={isSelected}
      suppressContentEditableWarning
      onBlur={handleBlur}
    >
      {name}
    </a>
  ) : (
    <span
      contentEditable={isSelected}
      suppressContentEditableWarning
      onBlur={handleBlur}
    >
      {name}
    </span>
  );

  return (
    <div className={className} data-block="core/file">
      {textNode}
      {showButton ? (
        <a
          href={attributes.href}
          className="wp-block-file__button"
          download
        >
          Download
        </a>
      ) : null}
    </div>
  );
}

export default FileEdit;
