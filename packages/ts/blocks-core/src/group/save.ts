/**
 * `core/group` save serializer + server-render hint.
 *
 * Generic container. Wraps inner blocks in a single element — the `tagName`
 * attribute lets authors pick between `<div>`, `<section>`, `<header>`,
 * `<footer>`, `<main>`, `<article>`, `<aside>`. The full server-side render
 * (with children inlined) is produced by `serverRender(attrs, innerHtml)`.
 */
import type { BlockAttributes, BlockSaveProps } from '@gonext/blocks-sdk';
import { classAttr } from '../internal/escape.ts';

/** Attribute shape for `core/group`. */
export interface GroupAttributes extends BlockAttributes {
  /** Wrapper element. Defaults to `div`. */
  tagName?: 'div' | 'section' | 'header' | 'footer' | 'main' | 'article' | 'aside';
  /** Layout strategy (themes hook into the resulting class). */
  layout?: 'default' | 'flex' | 'grid';
}

const INNER_SENTINEL = '<!--gn-inner-blocks-->';

function groupClasses(attrs: GroupAttributes): string[] {
  return [
    'gn-block-group',
    attrs.layout ? `is-layout-${attrs.layout}` : 'is-layout-default',
  ];
}

export function save({ attributes }: BlockSaveProps<GroupAttributes>): string {
  const tag = attributes.tagName ?? 'div';
  return `<${tag}${classAttr(groupClasses(attributes))}>${INNER_SENTINEL}</${tag}>`;
}

export function serverRender(attrs: GroupAttributes, innerHtml: string): string {
  const tag = attrs.tagName ?? 'div';
  return `<${tag}${classAttr(groupClasses(attrs))}>${innerHtml}</${tag}>`;
}

/** Exposed for the walker / tests. */
export const GROUP_INNER_SENTINEL = INNER_SENTINEL;
