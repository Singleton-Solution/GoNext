'use client';

/**
 * TokenReveal — the one-time modal that shows the plaintext PAT after
 * issuance.
 *
 * UX contract:
 *
 *  - Displays the plaintext token in a monospaced field with a
 *    copy-to-clipboard button.
 *  - The "Done" / dismiss action is GATED behind an "I've saved it"
 *    confirmation. The user must explicitly tick the box; the button
 *    is disabled otherwise. This deliberately friction-loads the
 *    happy path — losing a PAT is a silent failure that ends in
 *    "please regenerate", which is what we're trying to prevent.
 *  - The plaintext is masked by default and only revealed on click.
 *    A drive-by screenshot of the screen doesn't leak the secret to
 *    over-the-shoulder observers in shared workspaces.
 *  - The "Copy" action uses the async clipboard API; if it rejects
 *    (insecure context, permission denied), we fall back to a
 *    text-area + execCommand path so corporate sandboxes still work.
 *
 * The component is intentionally a leaf: it knows nothing about routing,
 * fetching, or where the token came from. The parent passes the
 * IssuedTokenView and an onDismiss callback. After dismiss, the parent
 * is responsible for navigating away — typically back to /settings/tokens.
 */

import type { ReactElement } from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';
import type { IssuedTokenView } from '../types';

export interface TokenRevealProps {
  /** The freshly-issued token. The plaintext is non-recoverable. */
  token: IssuedTokenView;
  /** Called once the user has confirmed they've saved the plaintext. */
  onDismiss: () => void;
}

export function TokenReveal({ token, onDismiss }: TokenRevealProps): ReactElement {
  const [revealed, setRevealed] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState<string | null>(null);
  const fallbackRef = useRef<HTMLTextAreaElement | null>(null);

  // Clear the "copied" pill after 2s so a second copy doesn't look
  // like a no-op.
  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(t);
  }, [copied]);

  const onCopy = useCallback(async () => {
    setCopyError(null);
    // Prefer the async clipboard API; fall back to execCommand for
    // browsers without it (and for jsdom in tests, which lacks the
    // permission grant).
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      try {
        await navigator.clipboard.writeText(token.plaintext);
        setCopied(true);
        return;
      } catch (err) {
        // Fall through to the textarea path.
        setCopyError(err instanceof Error ? err.message : 'clipboard denied');
      }
    }
    // Fallback: select-and-execCommand on a hidden textarea.
    const ta = fallbackRef.current;
    if (!ta) {
      setCopyError('clipboard unavailable');
      return;
    }
    ta.value = token.plaintext;
    ta.select();
    try {
      const ok = document.execCommand('copy');
      if (ok) {
        setCopied(true);
        setCopyError(null);
      } else {
        setCopyError('copy failed');
      }
    } catch (err) {
      setCopyError(err instanceof Error ? err.message : 'copy failed');
    }
  }, [token.plaintext]);

  return (
    <div className="token-reveal" role="dialog" aria-modal="true" aria-labelledby="token-reveal-title">
      <h2 id="token-reveal-title">Your new token</h2>

      <p className="muted">
        Treat this string like a password. <strong>Save it now</strong>
        {' '}— GoNext will never display it again. If you lose it, you’ll need
        to revoke this token and create a new one.
      </p>

      <div className="token-reveal__token-row">
        <input
          type={revealed ? 'text' : 'password'}
          readOnly
          value={token.plaintext}
          aria-label="Personal access token plaintext"
          data-testid="token-plaintext"
          className="token-reveal__token-field"
        />
        <button
          type="button"
          onClick={() => setRevealed((v) => !v)}
          aria-pressed={revealed}
          aria-label={revealed ? 'Hide token' : 'Show token'}
          className="token-reveal__reveal-btn"
        >
          {revealed ? 'Hide' : 'Show'}
        </button>
        <button
          type="button"
          onClick={onCopy}
          aria-label="Copy token to clipboard"
          className="token-reveal__copy-btn"
          data-testid="token-copy"
        >
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>

      {copyError && (
        <p role="status" className="token-reveal__error">
          Couldn’t reach the clipboard ({copyError}). Select the field above and copy manually.
        </p>
      )}

      <dl className="token-reveal__meta">
        <dt>Name</dt>
        <dd>{token.name}</dd>
        <dt>Prefix</dt>
        <dd>
          <code>{`gnp_${token.prefix}…`}</code>
        </dd>
        <dt>Scopes</dt>
        <dd>{token.scopes.join(', ') || '(none)'}</dd>
        {token.effective_scopes.length !== token.scopes.length && (
          <>
            <dt>Effective scopes</dt>
            <dd>
              {token.effective_scopes.join(', ') || '(none)'} {' '}
              <span className="muted">
                — narrower than requested because your role doesn’t grant the rest.
              </span>
            </dd>
          </>
        )}
        {token.expires_at && (
          <>
            <dt>Expires</dt>
            <dd>{new Date(token.expires_at).toLocaleString()}</dd>
          </>
        )}
      </dl>

      <label className="token-reveal__confirm">
        <input
          type="checkbox"
          checked={confirmed}
          onChange={(e) => setConfirmed(e.target.checked)}
          data-testid="token-confirm"
        />
        I’ve saved this token somewhere safe.
      </label>

      <div className="token-reveal__actions">
        <button
          type="button"
          onClick={onDismiss}
          disabled={!confirmed}
          data-testid="token-done"
          className="token-reveal__done-btn"
        >
          Done
        </button>
      </div>

      {/* Hidden textarea used by the execCommand clipboard fallback.
          Kept off-screen rather than display:none so the selection
          works. */}
      <textarea
        ref={fallbackRef}
        aria-hidden="true"
        tabIndex={-1}
        style={{ position: 'absolute', left: '-9999px', width: 1, height: 1 }}
        readOnly
      />
    </div>
  );
}
