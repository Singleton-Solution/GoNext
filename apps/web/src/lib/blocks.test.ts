/**
 * Tests for the block tree → HTML walker.
 *
 * Verifies:
 *  - Core blocks render through their serverRender hints.
 *  - Containers (group, columns) receive concatenated inner HTML.
 *  - Unknown blocks emit a `<!-- gn:unknown-block -->` marker but
 *    keep rendering rendered children behind it.
 *  - Plugin block registration adds a new handler.
 *  - User-supplied content is HTML-escaped.
 */
import { describe, it, expect } from 'vitest';
import type { Block } from '@gonext/blocks-sdk';
import { renderBlocks, registerBlock, getRegisteredHandler } from './blocks.ts';

describe('renderBlocks', () => {
  it('returns an empty string for an empty tree', () => {
    expect(renderBlocks([])).toBe('');
    expect(renderBlocks(null)).toBe('');
    expect(renderBlocks(undefined)).toBe('');
  });

  it('renders a single paragraph via core/paragraph serverRender', () => {
    const tree: Block[] = [
      { type: 'core/paragraph', attributes: { content: 'hello' } },
    ];
    const html = renderBlocks(tree);
    expect(html).toContain('<p');
    expect(html).toContain('gn-block-paragraph');
    expect(html).toContain('hello');
  });

  it('HTML-escapes user input inside text blocks', () => {
    const tree: Block[] = [
      {
        type: 'core/paragraph',
        attributes: { content: '<script>alert(1)</script>' },
      },
    ];
    const html = renderBlocks(tree);
    expect(html).not.toContain('<script>alert(1)</script>');
    expect(html).toContain('&lt;script&gt;');
  });

  it('renders heading levels through the tag attribute', () => {
    const tree: Block[] = [
      { type: 'core/heading', attributes: { content: 'Title', level: 2 } },
    ];
    const html = renderBlocks(tree);
    expect(html).toMatch(/<h2[^>]*>Title<\/h2>/);
  });

  it('concatenates sibling block output in order', () => {
    const tree: Block[] = [
      { type: 'core/paragraph', attributes: { content: 'one' } },
      { type: 'core/paragraph', attributes: { content: 'two' } },
    ];
    const html = renderBlocks(tree);
    const idxOne = html.indexOf('one');
    const idxTwo = html.indexOf('two');
    expect(idxOne).toBeGreaterThanOrEqual(0);
    expect(idxTwo).toBeGreaterThan(idxOne);
  });

  it('passes rendered inner HTML to container blocks', () => {
    const tree: Block[] = [
      {
        type: 'core/group',
        attributes: { tagName: 'section' },
        innerBlocks: [
          { type: 'core/paragraph', attributes: { content: 'inside' } },
        ],
      },
    ];
    const html = renderBlocks(tree);
    expect(html.startsWith('<section')).toBe(true);
    expect(html.endsWith('</section>')).toBe(true);
    expect(html).toContain('inside');
  });

  it('renders columns container with its 2-col modifier class', () => {
    const tree: Block[] = [
      {
        type: 'core/columns',
        attributes: { columns: 2 },
        innerBlocks: [
          { type: 'core/paragraph', attributes: { content: 'col-a' } },
          { type: 'core/paragraph', attributes: { content: 'col-b' } },
        ],
      },
    ];
    const html = renderBlocks(tree);
    expect(html).toContain('gn-block-columns--cols-2');
    expect(html).toContain('col-a');
    expect(html).toContain('col-b');
  });

  it('emits an unknown-block marker for unregistered types', () => {
    const tree: Block[] = [
      { type: 'plugin-x/widget', attributes: { key: 'value' } },
    ];
    const html = renderBlocks(tree);
    expect(html).toContain('<!-- gn:unknown-block name="plugin-x/widget" -->');
  });

  it('still renders inner blocks of an unknown container', () => {
    const tree: Block[] = [
      {
        type: 'plugin-x/unknown-wrap',
        attributes: {},
        innerBlocks: [
          { type: 'core/paragraph', attributes: { content: 'survived' } },
        ],
      },
    ];
    const html = renderBlocks(tree);
    expect(html).toContain('gn:unknown-block');
    expect(html).toContain('survived');
  });

  it('lets plugins register their own server-render handler', () => {
    registerBlock('test/badge', (attrs) => {
      const label = String((attrs as { label?: unknown }).label ?? '');
      return `<span class="badge">${label}</span>`;
    });
    expect(getRegisteredHandler('test/badge')).toBeDefined();
    const tree: Block[] = [
      { type: 'test/badge', attributes: { label: 'beta' } },
    ];
    expect(renderBlocks(tree)).toContain('<span class="badge">beta</span>');
  });

  it('skips malformed entries gracefully', () => {
    // Cast through unknown — the API contract says we receive Block[]
    // but defensive code shouldn't crash on garbage.
    const tree = [null, undefined, { not: 'a-block' }] as unknown as Block[];
    expect(() => renderBlocks(tree)).not.toThrow();
  });
});
