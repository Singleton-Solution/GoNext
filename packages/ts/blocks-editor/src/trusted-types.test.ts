/**
 * Tests for the blocks-editor Trusted Types primitive
 * (packages/ts/blocks-editor/src/trusted-types.ts).
 *
 * Coverage focus:
 *   - `installEditorPolicy` registers the gn-editor policy when
 *     `window.trustedTypes` is available.
 *   - `sanitizeBlockIcon` strips script vectors from inline SVG.
 *   - Inline `<script>` / `<foreignObject>` inside SVG is rejected.
 *   - In SSR / jsdom without trustedTypes the shim still sanitizes.
 *   - The policy is idempotent — repeat installs return the same
 *     instance and don't throw.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  GN_EDITOR_POLICY_NAME,
  __resetEditorPolicyForTests,
  installEditorPolicy,
  sanitizeBlockIcon,
} from './trusted-types.ts';

afterEach(() => {
  __resetEditorPolicyForTests();
  delete (globalThis as { trustedTypes?: unknown }).trustedTypes;
  vi.restoreAllMocks();
});

describe('installEditorPolicy', () => {
  it("registers under the 'gn-editor' policy name when trustedTypes is available", () => {
    const seen: string[] = [];
    (globalThis as { trustedTypes?: unknown }).trustedTypes = {
      createPolicy: (name: string, rules: { createHTML?: (input: string) => string }) => {
        seen.push(name);
        return { createHTML: rules.createHTML! };
      },
    };
    installEditorPolicy();
    expect(seen).toContain(GN_EDITOR_POLICY_NAME);
  });

  it('is idempotent — repeat calls return the same instance', () => {
    const a = installEditorPolicy();
    const b = installEditorPolicy();
    expect(a).toBe(b);
  });

  it('falls back to a shim that still sanitizes when createPolicy throws', () => {
    (globalThis as { trustedTypes?: unknown }).trustedTypes = {
      createPolicy: () => {
        throw new Error('duplicate');
      },
    };
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const policy = installEditorPolicy();
    expect(policy.createHTML('<svg></svg>')).toContain('<svg');
  });
});

describe('sanitizeBlockIcon', () => {
  it('preserves inline SVG icons', () => {
    const out = sanitizeBlockIcon('<svg><circle cx="5" cy="5" r="2"/></svg>');
    expect(out.__html).toContain('<svg');
    expect(out.__html).toContain('<circle');
  });

  it('strips <script> tags from raw input', () => {
    const out = sanitizeBlockIcon('<svg></svg><script>alert(1)</script>');
    expect(out.__html).not.toContain('<script');
  });

  it('strips onerror handlers from <image> inside SVG', () => {
    const out = sanitizeBlockIcon(
      '<svg><image href="x" onerror="alert(1)"/></svg>',
    );
    expect(out.__html).not.toContain('onerror');
  });

  it('strips <foreignObject> with arbitrary HTML payload', () => {
    const out = sanitizeBlockIcon(
      '<svg><foreignObject><iframe src="javascript:alert(1)"></iframe></foreignObject></svg>',
    );
    expect(out.__html).not.toContain('<iframe');
    expect(out.__html).not.toContain('javascript:alert');
  });

  it('returns a { __html } shape compatible with dangerouslySetInnerHTML', () => {
    const out = sanitizeBlockIcon('<svg></svg>');
    expect(out).toHaveProperty('__html');
    expect(typeof out.__html).toBe('string');
  });
});
