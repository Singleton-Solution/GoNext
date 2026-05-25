/**
 * `<SafeHTML>` — the only sanctioned React component for rendering
 * server-supplied HTML strings in the admin (issues #59, #90).
 *
 * Why a dedicated component:
 *   - The admin's CSP forces `require-trusted-types-for 'script'`, so
 *     any direct `innerHTML` assignment THROWS unless the value was
 *     minted by a registered Trusted Types policy.
 *   - `dangerouslySetInnerHTML` is BANNED across `apps/admin/src/`
 *     by ESLint (see .eslintrc.json). `<SafeHTML>` is the
 *     allowlisted alternative.
 *
 * The component renders an empty placeholder element on the SSR pass
 * and then runs `setHTML(ref.current, html)` once mounted, which:
 *   1. routes `html` through DOMPurify's strict admin profile
 *   2. funnels the cleaned string through the `gn-admin` (or
 *      `gn-editor`) Trusted Types policy
 *   3. assigns the resulting TrustedHTML to `innerHTML`
 *
 * The brief flash of empty content during hydration is acceptable for
 * the small set of admin surfaces that use this (search excerpts,
 * comment moderation previews) and is the safest cross-version pattern
 * — React 19 + Trusted Types interop is still a moving target.
 *
 * Usage:
 *
 *     <SafeHTML html={hit.excerpt_html} className="hit-excerpt" />
 *     <SafeHTML html={blockIconSVG} as="span" surface="editor" />
 */
'use client';

import React, { useEffect, useRef, type ElementType, type HTMLAttributes } from 'react';
import { setHTML, type PolicySurface } from '@/lib/trusted-types';

/**
 * Props for `<SafeHTML>`.
 *
 *   - `html`     The (potentially-untrusted) string to render. Routed
 *                through DOMPurify + the named Trusted Types policy.
 *   - `as`       Tag name to render. Defaults to `<span>` so callers
 *                rendering inside paragraph or button context don't
 *                introduce block-level boxes.
 *   - `surface`  Selects `gn-admin` (default) vs `gn-editor`. Use
 *                `editor` when rendering block icons or other rich
 *                editor content (allows inline SVG).
 *   - rest       Standard HTML props (className, id, role, etc.).
 */
export interface SafeHTMLProps extends HTMLAttributes<HTMLElement> {
  html: string;
  as?: ElementType;
  surface?: PolicySurface;
}

export function SafeHTML({
  html,
  as,
  surface = 'admin',
  ...rest
}: SafeHTMLProps): React.ReactElement {
  const ref = useRef<HTMLElement | null>(null);

  useEffect(() => {
    // setHTML internally sanitizes (DOMPurify) and routes through the
    // Trusted Types policy. Safe to call on every render; the helper
    // is idempotent in the no-change case (we still re-sanitize, but
    // the input is small enough that the cost is negligible).
    setHTML(ref.current, html, surface);
  }, [html, surface]);

  const Tag = (as ?? 'span') as ElementType;
  return <Tag ref={ref} {...rest} />;
}
