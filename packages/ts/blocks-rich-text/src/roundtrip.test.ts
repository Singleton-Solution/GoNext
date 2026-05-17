/**
 * Round-trip tests — the canonical property the rich-text package pins.
 *
 * For every HTML fragment in our supported subset, the chain
 *
 *   html ─deserializeInline→ runs ─serializeInline→ html'
 *
 * must produce `html' === html` (byte-for-byte). The same fragments are
 * what `core/paragraph`, `core/heading`, `core/quote`, and `core/list`
 * embed inside their block-level tags, so round-trip stability here
 * means the editor canvas → persisted store → SSR walker line up.
 */
import { describe, it, expect } from 'vitest';
import { serializeInline } from './serialize.ts';
import { deserializeInline } from './deserialize.ts';

const FIXTURES: ReadonlyArray<{ name: string; html: string }> = [
  { name: 'plain text', html: 'Hello, world.' },
  { name: 'escaped angle brackets', html: '&lt;script&gt;alert(1)&lt;/script&gt;' },
  { name: 'escaped ampersand', html: 'A &amp; B' },
  { name: 'single bold', html: '<strong>bold</strong>' },
  { name: 'single italic', html: '<em>italic</em>' },
  { name: 'single inline code', html: '<code>code()</code>' },
  {
    name: 'bold + italic + code stacked',
    html: '<strong><em><code>all</code></em></strong>',
  },
  {
    name: 'mark adjacent to plain text',
    html: 'plain <strong>bold</strong> plain',
  },
  { name: 'linebreak between text', html: 'line 1<br>line 2' },
  {
    name: 'link with rel + target',
    html: '<a href="https://example.com" rel="noopener" target="_blank">click</a>',
  },
  { name: 'internal link', html: '<a href="/about">About</a>' },
  {
    name: 'marks inside link',
    html: '<a href="/x"><strong>bold link</strong></a>',
  },
  {
    name: 'mixed content',
    html: 'See <a href="/x"><strong>here</strong></a> for more.',
  },
];

describe('round-trip: html → runs → html', () => {
  it.each(FIXTURES)('preserves $name', ({ html }) => {
    const runs = deserializeInline(html);
    const roundTripped = serializeInline(runs);
    expect(roundTripped).toBe(html);
  });

  it('also stabilises after two round-trips (idempotent)', () => {
    for (const { html } of FIXTURES) {
      const once = serializeInline(deserializeInline(html));
      const twice = serializeInline(deserializeInline(once));
      expect(twice).toBe(once);
    }
  });
});
