/**
 * `core/code` save serializer + server-render hint.
 *
 * Renders `<pre><code>` with an optional `language-*` class so highlight
 * libraries (Prism, Shiki, …) can pick up the language without reparsing.
 * The body is HTML-escaped — the renderer never trusts code content.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr, escapeHtml } from '../internal/escape.ts';

/** Attribute shape for `core/code`. */
export interface CodeAttributes extends BlockAttributes {
  /** Raw source content. Preserved verbatim, including newlines. */
  content: string;
  /** Highlighter language identifier (e.g. `ts`, `go`, `bash`). */
  language?: string;
}

function codeClasses(attrs: CodeAttributes): string[] {
  return [
    attrs.language ? `language-${attrs.language}` : null,
  ].filter((c): c is string => c !== null);
}

export function save({ attributes }: BlockSaveProps<CodeAttributes>): string {
  const codeClass = classAttr(codeClasses(attributes));
  return `<pre class="gn-block-code"><code${codeClass}>${escapeHtml(attributes.content)}</code></pre>`;
}

export function serverRender(attrs: CodeAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
