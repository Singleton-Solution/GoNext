/**
 * `core/navigation` tests — round-trip, schema validation, toggle behaviour,
 * nested-item rendering, and axe accessibility scan.
 */
import { describe, expect, it } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { BlockRegistry } from '@gonext/blocks-sdk';
import {
  DEFAULT_NAV_ARIA_LABEL,
  navigation,
  NavigationEdit,
} from './index.ts';
import { assertNoAxeViolations } from '../internal/axe.ts';

describe('core/navigation', () => {
  it('save() emits a <nav> with the default aria-label when none is set', () => {
    const html = navigation.save({ attributes: {} });
    expect(html).toContain('<nav');
    expect(html).toContain(`aria-label="${DEFAULT_NAV_ARIA_LABEL}"`);
  });

  it('save() renders each item as a list entry with an anchor', () => {
    const html = navigation.save({
      attributes: {
        items: [
          { label: 'Home', url: '/' },
          { label: 'Blog', url: '/blog' },
        ],
      },
    });
    expect(html).toContain('<a href="/">Home</a>');
    expect(html).toContain('<a href="/blog">Blog</a>');
    expect(html).toMatch(/<li[^>]*gn-block-navigation__item[^>]*>/);
  });

  it('save() collapses a URL-less item to a span, not an anchor', () => {
    const html = navigation.save({
      attributes: { items: [{ label: 'Coming soon', url: '' }] },
    });
    expect(html).toContain(
      '<span class="gn-block-navigation__item-label">Coming soon</span>',
    );
    expect(html).not.toContain('href=""');
  });

  it('save() renders nested children as a nested <ul>', () => {
    const html = navigation.save({
      attributes: {
        items: [
          {
            label: 'Docs',
            url: '/docs',
            children: [
              { label: 'API', url: '/docs/api' },
              { label: 'Guides', url: '/docs/guides' },
            ],
          },
        ],
      },
    });
    expect(html).toContain('has-submenu');
    expect(html).toContain('gn-block-navigation__submenu');
    expect(html).toContain('<a href="/docs/api">API</a>');
  });

  it('save() emits the toggle button by default with aria-controls', () => {
    const html = navigation.save({ attributes: { items: [] } });
    expect(html).toContain('gn-block-navigation__toggle');
    expect(html).toMatch(/aria-controls="gn-nav-menu"/);
    expect(html).toContain('aria-expanded="false"');
  });

  it('save() omits the toggle when hideToggle is true', () => {
    const html = navigation.save({
      attributes: { items: [], hideToggle: true },
    });
    expect(html).not.toContain('gn-block-navigation__toggle');
    expect(html).toContain('has-no-toggle');
  });

  it('save() honours orientation classes', () => {
    expect(
      navigation.save({ attributes: { orientation: 'vertical' } }),
    ).toContain('is-orientation-vertical');
    expect(
      navigation.save({ attributes: { orientation: 'horizontal' } }),
    ).toContain('is-orientation-horizontal');
  });

  it('save() escapes labels, urls, rel, and target attributes', () => {
    const html = navigation.save({
      attributes: {
        items: [
          {
            label: '<Home> & "main"',
            url: '/?a=b&c=d',
            rel: 'noopener "x"',
            target: '_blank',
          },
        ],
      },
    });
    expect(html).toContain('&lt;Home&gt; &amp; &quot;main&quot;');
    expect(html).toContain('/?a=b&amp;c=d');
    expect(html).toContain('rel="noopener &quot;x&quot;"');
    expect(html).toContain('target="_blank"');
  });

  it('save() applies a custom aria-label when set', () => {
    const html = navigation.save({
      attributes: { ariaLabel: 'Primary' },
    });
    expect(html).toContain('aria-label="Primary"');
  });

  it('validates a well-formed inline-items navigation block', () => {
    const r = new BlockRegistry();
    r.register(navigation.definition);
    expect(
      r.validate([
        {
          type: 'core/navigation',
          attributes: {
            items: [
              { label: 'Home', url: '/' },
              { label: 'About', url: '/about' },
            ],
          },
        },
      ]).valid,
    ).toBe(true);
  });

  it('validates a server-resolved menuId-only navigation block', () => {
    const r = new BlockRegistry();
    r.register(navigation.definition);
    expect(
      r.validate([
        {
          type: 'core/navigation',
          attributes: { menuId: 'primary' },
        },
      ]).valid,
    ).toBe(true);
  });

  it('rejects items missing the required label field', () => {
    const r = new BlockRegistry();
    r.register(navigation.definition);
    expect(
      r.validate([
        {
          type: 'core/navigation',
          attributes: { items: [{ url: '/x' }] },
        },
      ]).valid,
    ).toBe(false);
  });

  it('rejects an empty-string label as too short', () => {
    const r = new BlockRegistry();
    r.register(navigation.definition);
    expect(
      r.validate([
        {
          type: 'core/navigation',
          attributes: { items: [{ label: '', url: '/x' }] },
        },
      ]).valid,
    ).toBe(false);
  });

  it('rejects unknown orientation values', () => {
    const r = new BlockRegistry();
    r.register(navigation.definition);
    expect(
      r.validate([
        {
          type: 'core/navigation',
          attributes: { orientation: 'diagonal' },
        },
      ]).valid,
    ).toBe(false);
  });

  it('serverRender mirrors save for the inline-items case', () => {
    const attrs = {
      items: [
        { label: 'Home', url: '/' },
        { label: 'Blog', url: '/blog' },
      ],
    };
    expect(navigation.serverRender(attrs, '')).toBe(
      navigation.save({ attributes: attrs }),
    );
  });

  it('snapshot: default navigation, no items', () => {
    expect(navigation.save({ attributes: {} })).toMatchSnapshot();
  });

  it('snapshot: vertical orientation with nested children', () => {
    expect(
      navigation.save({
        attributes: {
          orientation: 'vertical',
          ariaLabel: 'Footer',
          items: [
            {
              label: 'Company',
              url: '/about',
              children: [
                { label: 'Team', url: '/team' },
                { label: 'Careers', url: '/careers' },
              ],
            },
            { label: 'Contact', url: '/contact' },
          ],
        },
      }),
    ).toMatchSnapshot();
  });

  it('Edit component renders the items and the toggle starts collapsed', () => {
    const { container, getByText } = render(
      <NavigationEdit
        attributes={{
          items: [
            { label: 'Home', url: '/' },
            { label: 'Blog', url: '/blog' },
          ],
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="nav-1"
        context={{}}
      />,
    );
    expect(getByText('Home')).toBeTruthy();
    expect(getByText('Blog')).toBeTruthy();
    const toggle = container.querySelector(
      'button.gn-block-navigation__toggle',
    );
    expect(toggle?.getAttribute('aria-expanded')).toBe('false');
  });

  it('Edit toggle button flips aria-expanded on click', () => {
    const { container } = render(
      <NavigationEdit
        attributes={{ items: [{ label: 'Home', url: '/' }] }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="nav-2"
        context={{}}
      />,
    );
    const toggle = container.querySelector(
      'button.gn-block-navigation__toggle',
    ) as HTMLButtonElement;
    expect(toggle).toBeTruthy();
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(container.firstChild).toHaveProperty('className');
    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
  });

  it('Edit component omits the toggle when hideToggle is set', () => {
    const { container } = render(
      <NavigationEdit
        attributes={{
          items: [{ label: 'Home', url: '/' }],
          hideToggle: true,
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="nav-3"
        context={{}}
      />,
    );
    expect(
      container.querySelector('.gn-block-navigation__toggle'),
    ).toBeNull();
  });

  it('Edit component shows an empty placeholder when no items are set', () => {
    const { getByText } = render(
      <NavigationEdit
        attributes={{}}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="nav-empty"
        context={{}}
      />,
    );
    expect(getByText('Add menu items')).toBeTruthy();
  });

  // Issue #250 — WCAG 2.1 AA: every interactive surface must score clean.
  it('Edit component has no axe a11y violations', async () => {
    const { container } = render(
      <NavigationEdit
        attributes={{
          items: [
            { label: 'Home', url: '/' },
            { label: 'Blog', url: '/blog' },
            {
              label: 'Docs',
              url: '/docs',
              children: [{ label: 'API', url: '/docs/api' }],
            },
          ],
        }}
        setAttributes={() => undefined}
        isSelected={false}
        clientId="nav-axe"
        context={{}}
      />,
    );
    await assertNoAxeViolations(container);
  });
});
