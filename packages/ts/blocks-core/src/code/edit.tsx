/**
 * `core/code` Edit component.
 *
 * Renders a contenteditable `<pre><code>` block so authors can type code
 * inline. The component preserves whitespace by default — `<pre>` keeps
 * newlines and indentation as authored.
 */
import type { BlockEditProps } from '@gonext/blocks-sdk';
import type { ReactElement } from 'react';
import type { CodeAttributes } from './save.ts';

export function CodeEdit({
  attributes,
  setAttributes,
  isSelected,
}: BlockEditProps<CodeAttributes>): ReactElement {
  const codeClassName = attributes.language
    ? `language-${attributes.language}`
    : undefined;

  return (
    <pre
      className={
        ['gn-block-code', isSelected ? 'is-selected' : null]
          .filter((c): c is string => c !== null)
          .join(' ')
      }
      data-block="core/code"
    >
      <code
        className={codeClassName}
        contentEditable={isSelected}
        suppressContentEditableWarning
        onInput={(event) =>
          setAttributes({
            content: (event.target as HTMLElement).textContent ?? '',
          })
        }
      >
        {attributes.content || '// your code here'}
      </code>
    </pre>
  );
}

export default CodeEdit;
