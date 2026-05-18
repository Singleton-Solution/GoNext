/**
 * Tests for the <PluginScript> React component.
 *
 * We render through React Testing Library's `render` helper into the
 * jsdom DOM. Script tags don't execute under jsdom (no real script
 * loader) but the element attributes are visible via the test DOM,
 * which is exactly what we want to pin.
 */
import { render } from '@testing-library/react';
import { renderToStaticMarkup } from 'react-dom/server';
import type { ReactElement } from 'react';
import { describe, expect, it } from 'vitest';
import { PluginScript } from './script-tag';

// A known-valid sha384 hash so isValidSRIAttribute accepts the prop.
const VALID_HASH =
  'sha384-AxDMIqo9dfV5IUTjnquaoPQ0ZJmXM5LaPiNZXfVTrejh97+msKLNSHd7SpJtmBkv';

/**
 * Render helper that returns the rendered <script> element. RTL's
 * default container doesn't expose script tags via getByRole — we
 * reach in via querySelector instead.
 */
function renderScript(node: ReactElement): HTMLScriptElement {
  const { container } = render(node);
  const el = container.querySelector('script');
  if (el === null) {
    throw new Error('no script element in render output');
  }
  return el as HTMLScriptElement;
}

describe('<PluginScript>', () => {
  it('renders a type="module" script with the supplied src + integrity', () => {
    const el = renderScript(
      <PluginScript src="/_/plugins/sample/index.mjs" hash={VALID_HASH} />,
    );
    expect(el.getAttribute('type')).toBe('module');
    expect(el.getAttribute('src')).toBe('/_/plugins/sample/index.mjs');
    expect(el.getAttribute('integrity')).toBe(VALID_HASH);
  });

  it('sets crossorigin="anonymous" so SRI checking actually runs', () => {
    const el = renderScript(<PluginScript src="/x.mjs" hash={VALID_HASH} />);
    expect(el.getAttribute('crossorigin')).toBe('anonymous');
  });

  it('tags the script with the gn-plugin Trusted Types policy name (debug hint)', () => {
    const el = renderScript(<PluginScript src="/x.mjs" hash={VALID_HASH} />);
    expect(el.getAttribute('data-trusted-types-policy')).toBe('gn-plugin');
  });

  it('OMITS nonce by default (plugin scripts trust integrity, not nonce)', () => {
    const el = renderScript(<PluginScript src="/x.mjs" hash={VALID_HASH} />);
    expect(el.hasAttribute('nonce')).toBe(false);
  });

  it('emits nonce only when explicitly supplied (for test fixtures)', () => {
    const el = renderScript(
      <PluginScript src="/x.mjs" hash={VALID_HASH} nonce="ABC123" />,
    );
    expect(el.getAttribute('nonce')).toBe('ABC123');
  });

  it('honors the defer prop', () => {
    const el = renderScript(<PluginScript src="/x.mjs" hash={VALID_HASH} defer />);
    expect(el.hasAttribute('defer')).toBe(true);
  });

  it('honors the async prop', () => {
    // jsdom's parser fetches async scripts out of the DOM, so render
    // through renderToStaticMarkup which gives us the raw HTML string
    // exactly as the server would emit it.
    const html = renderToStaticMarkup(
      <PluginScript src="/x.mjs" hash={VALID_HASH} async />,
    );
    expect(html).toMatch(/async/);
  });

  it('forwards id to the rendered element', () => {
    const el = renderScript(
      <PluginScript src="/x.mjs" hash={VALID_HASH} id="my-plugin" />,
    );
    expect(el.id).toBe('my-plugin');
  });

  it('forwards data-* attributes verbatim', () => {
    const el = renderScript(
      <PluginScript
        src="/x.mjs"
        hash={VALID_HASH}
        dataAttributes={{ 'data-plugin': 'sample', version: '1.2.3' }}
      />,
    );
    expect(el.getAttribute('data-plugin')).toBe('sample');
    expect(el.getAttribute('data-version')).toBe('1.2.3');
  });

  it('throws synchronously when integrity hash is malformed', () => {
    expect(() => render(<PluginScript src="/x.mjs" hash="not-an-sri" />)).toThrowError(
      /invalid integrity hash/,
    );
  });

  it('throws synchronously when src is empty', () => {
    expect(() => render(<PluginScript src="" hash={VALID_HASH} />)).toThrowError(
      /requires a non-empty src/,
    );
  });
});
