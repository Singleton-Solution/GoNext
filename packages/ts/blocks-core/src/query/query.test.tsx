/**
 * `core/query` tests — round-trip, schema validation, snapshot, and the
 * data-attribute contract the server walker reads.
 */
import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import {
  query,
  QueryEdit,
  QUERY_DEFAULTS,
  QUERY_INNER_SENTINEL,
  summariseQuery,
} from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/query', () => {
  it('save() emits the inner-blocks sentinel inside the wrapper', () => {
    const html = query.save({ attributes: {} });
    expect(html).toContain(QUERY_INNER_SENTINEL);
    expect(html).toMatch(/<ul[^>]*>.*<!--gn-query-loop-->.*<\/ul>/);
  });

  it('save() defaults to a <ul> wrapper', () => {
    const html = query.save({ attributes: {} });
    expect(html.startsWith('<ul')).toBe(true);
    expect(html.endsWith('</ul>')).toBe(true);
  });

  it('save() honours tagName="div"', () => {
    const html = query.save({ attributes: { tagName: 'div' } });
    expect(html.startsWith('<div')).toBe(true);
    expect(html.endsWith('</div>')).toBe(true);
  });

  it('save() defaults limit, offset, order, and orderBy when omitted', () => {
    const html = query.save({ attributes: {} });
    expect(html).toContain(`data-gn-query-limit="${QUERY_DEFAULTS.limit}"`);
    expect(html).toContain(`data-gn-query-offset="${QUERY_DEFAULTS.offset}"`);
    expect(html).toContain(`data-gn-query-order="${QUERY_DEFAULTS.order}"`);
    expect(html).toContain(
      `data-gn-query-order-by="${QUERY_DEFAULTS.orderBy}"`,
    );
  });

  it('save() reflects filter attributes as discrete data-* tokens', () => {
    const html = query.save({
      attributes: {
        limit: 5,
        offset: 10,
        authorId: 'u-7',
        category: 'engineering',
        tag: 'release',
        search: 'editor',
        order: 'asc',
        orderBy: 'title',
        sticky: true,
      },
    });
    expect(html).toContain('data-gn-query-limit="5"');
    expect(html).toContain('data-gn-query-offset="10"');
    expect(html).toContain('data-gn-query-author="u-7"');
    expect(html).toContain('data-gn-query-category="engineering"');
    expect(html).toContain('data-gn-query-tag="release"');
    expect(html).toContain('data-gn-query-search="editor"');
    expect(html).toContain('data-gn-query-order="asc"');
    expect(html).toContain('data-gn-query-order-by="title"');
    expect(html).toContain('data-gn-query-sticky="true"');
  });

  it('save() omits filter data-* attributes when their attribute is unset', () => {
    const html = query.save({ attributes: { limit: 3 } });
    expect(html).not.toContain('data-gn-query-author');
    expect(html).not.toContain('data-gn-query-category');
    expect(html).not.toContain('data-gn-query-tag');
    expect(html).not.toContain('data-gn-query-search');
    expect(html).not.toContain('data-gn-query-sticky');
  });

  it('save() escapes filter values so user input cannot break out of an attribute', () => {
    const html = query.save({
      attributes: {
        category: '" onclick="x"',
        search: '<script>',
      },
    });
    expect(html).toContain('&quot; onclick=&quot;x&quot;');
    expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('<script>');
  });

  it('save() emits class names reflecting order and orderBy', () => {
    const html = query.save({
      attributes: { order: 'asc', orderBy: 'title' },
    });
    expect(html).toContain('is-order-asc');
    expect(html).toContain('is-order-by-title');
  });

  it('serverRender substitutes innerHtml into the sentinel slot', () => {
    const rendered = query.serverRender(
      { limit: 2 },
      '<li>Post one</li><li>Post two</li>',
    );
    expect(rendered).toContain('<li>Post one</li>');
    expect(rendered).toContain('<li>Post two</li>');
    expect(rendered).not.toContain(QUERY_INNER_SENTINEL);
  });

  it('serverRender keeps the wrapper when innerHtml is empty (zero-results)', () => {
    const rendered = query.serverRender({}, '');
    expect(rendered).toContain('wp-block-query');
    expect(rendered).toContain(`data-gn-query-limit="${QUERY_DEFAULTS.limit}"`);
  });

  it('validates a minimal query block', () => {
    const r = new BlockRegistry();
    r.register(query.definition);
    expect(
      r.validate([{ type: 'core/query', attributes: {} }]).valid,
    ).toBe(true);
  });

  it('rejects limit > 50 to bound a single render pass', () => {
    const r = new BlockRegistry();
    r.register(query.definition);
    expect(
      r.validate([
        { type: 'core/query', attributes: { limit: 100 } },
      ]).valid,
    ).toBe(false);
  });

  it('rejects limit < 1', () => {
    const r = new BlockRegistry();
    r.register(query.definition);
    expect(
      r.validate([
        { type: 'core/query', attributes: { limit: 0 } },
      ]).valid,
    ).toBe(false);
  });

  it('rejects negative offset', () => {
    const r = new BlockRegistry();
    r.register(query.definition);
    expect(
      r.validate([
        { type: 'core/query', attributes: { offset: -1 } },
      ]).valid,
    ).toBe(false);
  });

  it('rejects unknown order direction values', () => {
    const r = new BlockRegistry();
    r.register(query.definition);
    expect(
      r.validate([
        { type: 'core/query', attributes: { order: 'random' } },
      ]).valid,
    ).toBe(false);
  });

  it('rejects unknown orderBy fields', () => {
    const r = new BlockRegistry();
    r.register(query.definition);
    expect(
      r.validate([
        { type: 'core/query', attributes: { orderBy: 'random' } },
      ]).valid,
    ).toBe(false);
  });

  it('summariseQuery falls back to defaults when filters are absent', () => {
    expect(summariseQuery({})).toBe('Up to 10 posts · date desc');
  });

  it('summariseQuery lists active filters in the chip text', () => {
    expect(
      summariseQuery({
        limit: 6,
        category: 'engineering',
        tag: 'release',
        order: 'asc',
        orderBy: 'title',
      }),
    ).toBe(
      'Up to 6 posts · category: engineering, tag: release · title asc',
    );
  });

  it('snapshot: default query', () => {
    expect(query.save({ attributes: {} })).toMatchSnapshot();
  });

  it('snapshot: filtered query with custom order', () => {
    expect(
      query.save({
        attributes: {
          limit: 5,
          authorId: 'jane',
          category: 'news',
          order: 'asc',
          orderBy: 'title',
          sticky: true,
        },
      }),
    ).toMatchSnapshot();
  });

  it('supports.innerBlocks is true so the editor accepts the post-card template', () => {
    expect(query.definition.supports?.innerBlocks).toBe(true);
  });

  it('Edit component renders the placeholder summary chip + template slot', () => {
    const { container, getByText } = render(
      <QueryEdit
        attributes={{ limit: 5, category: 'news' }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="q-1"
        context={{}}
      />,
    );
    expect(getByText('Query Loop')).toBeTruthy();
    expect(getByText(/Up to 5 posts/)).toBeTruthy();
    expect(
      container.querySelector('[data-gn-inner-blocks="template"]'),
    ).toBeTruthy();
  });

  it('Edit component wrapper carries the order / orderBy classes', () => {
    const { container } = render(
      <QueryEdit
        attributes={{ order: 'asc', orderBy: 'title' }}
        setAttributes={() => undefined}
        isSelected={true}
        clientId="q-2"
        context={{}}
      />,
    );
    const root = container.querySelector('[data-block="core/query"]');
    expect(root?.className).toContain('is-order-asc');
    expect(root?.className).toContain('is-order-by-title');
    expect(root?.className).toContain('is-selected');
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <QueryEdit
        attributes={{ limit: 3 }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="q-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
