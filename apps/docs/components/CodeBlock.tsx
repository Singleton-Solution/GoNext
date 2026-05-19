/**
 * Code block wrapper for MDX-routed pages.
 *
 * Shiki produces the final `<pre><code>` markup at the renderer layer
 * (`lib/mdx.tsx`), but for first-party MDX pages — landing, future API
 * reference — we want a tiny client-rendered wrapper that adds a Copy
 * button without re-tokenising the source.
 */
'use client';

import { useState, type ReactElement, type ReactNode } from 'react';

interface CodeBlockProps {
  /** Optional caption/filename rendered above the code. */
  filename?: string;
  /** The pre-formatted code block; typically a `<pre>` child of MDX. */
  children: ReactNode;
}

export function CodeBlock({ filename, children }: CodeBlockProps): ReactElement {
  const [copied, setCopied] = useState(false);

  const onCopy = async () => {
    // The `children` is the rendered `<pre>` tree; extract its text content
    // by stringifying through a transient DOM node. Cheap enough for the
    // tiny snippets in a docs site.
    if (typeof document === 'undefined') return;
    const text = (document.activeElement?.parentElement?.querySelector('pre')?.textContent ?? '').trim();
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Older browsers / insecure contexts — no-op. The text is still
      // selectable so this is a soft failure.
    }
  };

  return (
    <figure className="code-block">
      <div className="code-block__bar">
        <span className="code-block__filename">{filename ?? ''}</span>
        <button type="button" className="code-block__copy" onClick={onCopy}>
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <div className="code-block__body">{children}</div>
    </figure>
  );
}
