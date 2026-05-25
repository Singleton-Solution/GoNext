'use client';

/**
 * TokenReveal — the one-time modal that surfaces a freshly-minted PAT.
 *
 * Restyled with the Living-Systems brand:
 *  - Card on paper-2 with an Archivo headline carrying the italic-accent
 *    rule ("Your new *token*.").
 *  - Plaintext field renders in Geist Mono — credentials want the "this
 *    is data, not prose" cue.
 *  - Show/Hide and Copy buttons sit in the same row; Copy uses the
 *    Lucide Copy icon and the emerald primary CTA.
 *  - The dismissal button is gated behind an "I've saved it" checkbox.
 *    Once ticked, the button becomes the emerald confirm CTA.
 *
 * UX contract (preserved verbatim from the pre-brand implementation):
 *
 *  - Plaintext masked by default; click Show to reveal.
 *  - Copy uses async clipboard API with execCommand fallback.
 *  - "Done" is disabled until the confirmation box is ticked.
 *  - effective_scopes mismatch surfaces an inline warning.
 *
 * All data-testid attributes are kept so the TokenReveal.test.tsx suite
 * continues to pin the security-sensitive behaviours.
 */

import type { ReactElement } from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Check, Copy, Eye, EyeOff, ShieldAlert } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Headline } from '@/components/ui/headline';
import { cn } from '@/lib/utils';

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

  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(t);
  }, [copied]);

  const onCopy = useCallback(async () => {
    setCopyError(null);
    if (
      navigator.clipboard &&
      typeof navigator.clipboard.writeText === 'function'
    ) {
      try {
        await navigator.clipboard.writeText(token.plaintext);
        setCopied(true);
        return;
      } catch (err) {
        setCopyError(err instanceof Error ? err.message : 'clipboard denied');
      }
    }
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

  const scopesMatch = token.effective_scopes.length === token.scopes.length;

  return (
    <Card
      className="border-border bg-paper-2 shadow-lg"
      role="dialog"
      aria-modal="true"
      aria-labelledby="token-reveal-title"
    >
      <CardContent className="flex flex-col gap-5 p-6">
        <div className="flex flex-col gap-2">
          <span className="inline-flex items-center gap-1.5 font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
            <Check className="h-3.5 w-3.5" aria-hidden="true" />
            Token issued
          </span>
          <Headline id="token-reveal-title" as="h2" size="sub">
            Your new <em>token</em>.
          </Headline>
          <p className="font-sans text-sm text-fg-muted">
            Treat this string like a password.{' '}
            <strong className="text-ink">Save it now</strong> — GoNext will
            never display it again. If you lose it, you’ll need to revoke
            this token and create a new one.
          </p>
        </div>

        <div className="flex flex-col gap-2">
          <label
            htmlFor="token-plaintext"
            className="font-sans text-xs font-medium uppercase tracking-[0.06em] text-fg-subtle"
          >
            Plaintext token
          </label>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <input
              id="token-plaintext"
              type={revealed ? 'text' : 'password'}
              readOnly
              value={token.plaintext}
              aria-label="Personal access token plaintext"
              data-testid="token-plaintext"
              className={cn(
                'flex-1 rounded-md border border-border bg-paper-3 px-3 py-2 font-mono text-sm text-ink',
                'focus-visible:border-emerald focus-visible:shadow-focus focus-visible:outline-none',
              )}
            />
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="default"
                size="sm"
                onClick={() => setRevealed((v) => !v)}
                aria-pressed={revealed}
                aria-label={revealed ? 'Hide token' : 'Show token'}
              >
                {revealed ? (
                  <>
                    <EyeOff className="h-3.5 w-3.5" aria-hidden="true" />
                    Hide
                  </>
                ) : (
                  <>
                    <Eye className="h-3.5 w-3.5" aria-hidden="true" />
                    Show
                  </>
                )}
              </Button>
              <Button
                type="button"
                variant="emerald"
                size="sm"
                onClick={onCopy}
                aria-label="Copy token to clipboard"
                data-testid="token-copy"
              >
                {copied ? (
                  <>
                    <Check className="h-3.5 w-3.5" aria-hidden="true" />
                    Copied!
                  </>
                ) : (
                  <>
                    <Copy className="h-3.5 w-3.5" aria-hidden="true" />
                    Copy
                  </>
                )}
              </Button>
            </div>
          </div>
          {copyError && (
            <p
              role="status"
              className="rounded-md border border-warning/30 bg-warning-soft px-3 py-2 font-sans text-xs text-warning"
            >
              Couldn’t reach the clipboard ({copyError}). Select the field
              above and copy manually.
            </p>
          )}
        </div>

        <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 rounded-md border border-border bg-paper-3 px-4 py-3 font-sans text-sm">
          <dt className="font-medium text-fg-subtle">Name</dt>
          <dd className="text-ink">{token.name}</dd>
          <dt className="font-medium text-fg-subtle">Prefix</dt>
          <dd>
            <code className="rounded-sm bg-paper-2 px-1.5 py-0.5 font-mono text-xs text-ink">
              gnp_{token.prefix}…
            </code>
          </dd>
          <dt className="font-medium text-fg-subtle">Scopes</dt>
          <dd className="flex flex-wrap gap-1">
            {token.scopes.length === 0 ? (
              <span className="text-fg-muted">(none)</span>
            ) : (
              token.scopes.map((scope) => (
                <Badge key={scope} variant="lavender">
                  {scope}
                </Badge>
              ))
            )}
          </dd>
          {!scopesMatch && (
            <>
              <dt className="flex items-center gap-1 font-medium text-warning">
                <ShieldAlert
                  className="h-3.5 w-3.5"
                  aria-hidden="true"
                />
                Effective scopes
              </dt>
              <dd className="flex flex-col gap-1">
                <div className="flex flex-wrap gap-1">
                  {token.effective_scopes.length === 0 ? (
                    <span className="text-fg-muted">(none)</span>
                  ) : (
                    token.effective_scopes.map((scope) => (
                      <Badge key={scope} variant="warning">
                        {scope}
                      </Badge>
                    ))
                  )}
                </div>
                <span className="font-sans text-xs text-fg-muted">
                  Narrower than requested because your role doesn’t grant the
                  rest.
                </span>
              </dd>
            </>
          )}
          {token.expires_at && (
            <>
              <dt className="font-medium text-fg-subtle">Expires</dt>
              <dd className="font-mono text-xs text-fg-muted">
                {new Date(token.expires_at).toLocaleString()}
              </dd>
            </>
          )}
        </dl>

        <label className="flex cursor-pointer items-center gap-2 rounded-md border border-border bg-paper-3 px-3 py-2.5 font-sans text-sm text-ink">
          <input
            type="checkbox"
            checked={confirmed}
            onChange={(e) => setConfirmed(e.target.checked)}
            data-testid="token-confirm"
            className="h-4 w-4 cursor-pointer accent-emerald"
          />
          I’ve saved this token somewhere safe.
        </label>

        <div className="flex items-center justify-end border-t border-border pt-4">
          <Button
            type="button"
            variant="emerald"
            onClick={onDismiss}
            disabled={!confirmed}
            data-testid="token-done"
          >
            <Check className="h-4 w-4" aria-hidden="true" />
            Done
          </Button>
        </div>

        <textarea
          ref={fallbackRef}
          aria-hidden="true"
          tabIndex={-1}
          style={{
            position: 'absolute',
            left: '-9999px',
            width: 1,
            height: 1,
          }}
          readOnly
        />
      </CardContent>
    </Card>
  );
}
