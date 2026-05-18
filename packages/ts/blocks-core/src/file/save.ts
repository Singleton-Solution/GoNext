/**
 * `core/file` save serializer + server-render hint.
 *
 * The file block surfaces a downloadable asset. The persisted markup
 * mirrors WordPress's:
 *
 *   <div class="wp-block-file">
 *     <a href="…">FileName</a>
 *     <a href="…" class="wp-block-file__button" download>Download</a>
 *   </div>
 *
 * The two link toggles (`textLinkHref`, `downloadButton`) control which
 * children render — the text link is the human-readable name; the button
 * is the explicit download CTA.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/file`. */
export interface FileAttributes extends BlockAttributes {
  /** Asset URL. */
  href: string;
  /** Human-readable file name displayed in the link. */
  fileName: string;
  /** When true, render the explicit "Download" button. Defaults to true. */
  downloadButton?: boolean;
  /** When true, render the file name as a clickable link. Defaults to true. */
  textLinkHref?: boolean;
}

function fileClasses(_attrs: FileAttributes): string[] {
  return ['wp-block-file'];
}

/**
 * Pure serializer. Both child links share the same href. The download
 * button gets the WP-style class and a `download` attribute so the
 * browser triggers a save dialog instead of navigation.
 */
export function save({ attributes }: BlockSaveProps<FileAttributes>): string {
  const href = escapeHtml(attributes.href);
  const name = escapeHtml(attributes.fileName);
  const showLink = attributes.textLinkHref !== false;
  const showButton = attributes.downloadButton !== false;
  const textLink = showLink
    ? `<a href="${href}">${name}</a>`
    : `<span>${name}</span>`;
  const downloadButton = showButton
    ? `<a href="${href}" class="wp-block-file__button" download>Download</a>`
    : '';
  return `<div${classAttr(fileClasses(attributes))}>${textLink}${downloadButton}</div>`;
}

export function serverRender(
  attrs: FileAttributes,
  _innerHtml: string,
): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
