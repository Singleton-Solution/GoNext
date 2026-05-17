/**
 * `core/separator` save serializer + server-render hint.
 *
 * Pure visual divider. Renders as `<hr/>` with a style variant the theme
 * can hook into via `is-style-*` classes.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr } from '../internal/escape.ts';

/** Attribute shape for `core/separator`. */
export interface SeparatorAttributes extends BlockAttributes {
  /** Style variant: `default`, `wide`, `dots`. */
  style?: 'default' | 'wide' | 'dots';
}

function separatorClasses(attrs: SeparatorAttributes): string[] {
  return [
    'gn-block-separator',
    attrs.style ? `is-style-${attrs.style}` : 'is-style-default',
  ];
}

export function save({ attributes }: BlockSaveProps<SeparatorAttributes>): string {
  return `<hr${classAttr(separatorClasses(attributes))}/>`;
}

export function serverRender(attrs: SeparatorAttributes, _innerHtml: string): string {
  void _innerHtml;
  return save({ attributes: attrs });
}
