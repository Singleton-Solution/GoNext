/**
 * Tests for the PublicShell envelope component.
 *
 * Verifies it forwards the trusted HTML strings into the DOM
 * unchanged and stamps the template basename onto the data attribute
 * e2e tests assert on.
 */
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { PublicShell } from './PublicShell';

describe('PublicShell', () => {
  it('renders the body HTML verbatim', () => {
    const { container } = render(
      <PublicShell
        bodyHtml="<header>H</header><main>M</main><footer>F</footer>"
        cssCustomProperties=":root{--x:1}"
        templateBasename="single.html"
      />,
    );
    expect(container.querySelector('header')).not.toBeNull();
    expect(container.querySelector('main')?.textContent).toBe('M');
    expect(container.querySelector('footer')).not.toBeNull();
  });

  it('inlines the theme CSS custom properties in a <style data-gn-theme>', () => {
    const { container } = render(
      <PublicShell
        bodyHtml=""
        cssCustomProperties=":root{--y:2}"
        templateBasename="index.html"
      />,
    );
    const style = container.querySelector('style[data-gn-theme]');
    expect(style?.innerHTML).toBe(':root{--y:2}');
  });

  it('stamps the template basename onto the outer wrapper', () => {
    const { container } = render(
      <PublicShell
        bodyHtml="<p>hi</p>"
        cssCustomProperties=""
        templateBasename="archive-book.tsx"
      />,
    );
    const site = container.querySelector('.gn-site');
    expect(site?.getAttribute('data-gn-template')).toBe('archive-book.tsx');
  });
});
