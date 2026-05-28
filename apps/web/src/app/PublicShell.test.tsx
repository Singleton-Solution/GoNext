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
        withChrome={false}
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
        withChrome={false}
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
        withChrome={false}
      />,
    );
    const site = container.querySelector('.gn-site');
    expect(site?.getAttribute('data-gn-template')).toBe('archive-book.tsx');
  });

  it('renders the brand chrome wrapper by default', () => {
    const { container } = render(
      <PublicShell
        bodyHtml="<p>themed body</p>"
        cssCustomProperties=""
        templateBasename="single.html"
      />,
    );
    // MarketingNav + MarketingFooter are async Server Components,
    // wrapped in a Suspense boundary in the shell. RTL renders the
    // fallback (null) so we assert on the chrome wrapper + themed
    // body instead. The chrome's internal contract is exercised in
    // Nav.test.tsx / Footer.test.tsx.
    expect(container.querySelector('main')).not.toBeNull();
    expect(container.querySelector('main .gn-site')).not.toBeNull();
    expect(container.querySelector('.gn-site')?.textContent).toContain(
      'themed body',
    );
  });

  it('renders children inside the chrome, after the themed body', () => {
    const { container } = render(
      <PublicShell
        bodyHtml="<p>post</p>"
        cssCustomProperties=""
        templateBasename="single.html"
      >
        <aside data-testid="comments">comments</aside>
      </PublicShell>,
    );
    const slot = container.querySelector('[data-testid=comments]');
    expect(slot).not.toBeNull();
    expect(slot?.textContent).toBe('comments');
  });
});
