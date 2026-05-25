'use client';

/**
 * NewTokenForm — issue a new Personal Access Token.
 *
 * Restyled with the Living-Systems brand:
 *  - Name input on paper-3 surface with emerald focus halo.
 *  - Scopes render as lavender-accented checkbox cards so the multi-select
 *    feels like the chip surface on the token list.
 *  - Expiry presets as radio cards on paper-3.
 *  - The "Generate token" submit is the emerald primary CTA.
 *
 * Behaviour is untouched from the pre-brand implementation; the data
 * contract with `issueToken()` is identical. Tests in NewTokenForm.test.tsx
 * key off data-testid attributes which we preserve verbatim.
 */

import type { FormEvent, ReactElement } from 'react';
import { useCallback, useMemo, useState } from 'react';
import { Key, ShieldCheck } from 'lucide-react';

import { ApiError } from '@/lib/api-client';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';

import { issueToken } from '../api';
import type { ExpiresPreset, IssuedTokenView } from '../types';
import { SCOPE_OPTIONS } from '../types';

export interface NewTokenFormProps {
  /**
   * Called with the issued token on success. Parent shows TokenReveal.
   */
  onIssued: (token: IssuedTokenView) => void;
}

const EXPIRY_PRESETS: ReadonlyArray<{
  value: ExpiresPreset;
  label: string;
  hint: string;
}> = [
  {
    value: '30d',
    label: '30 days',
    hint: 'Short-lived; recommended for one-off scripts.',
  },
  {
    value: '90d',
    label: '90 days',
    hint: 'Quarterly rotation; good for CI tokens reviewed at each release.',
  },
  {
    value: '1y',
    label: '1 year',
    hint: 'Long-lived; pair with calendar reminders.',
  },
  {
    value: 'never',
    label: 'No expiry',
    hint: 'Never expires. Only use for tokens you actively monitor.',
  },
];

export function NewTokenForm({ onIssued }: NewTokenFormProps): ReactElement {
  const [name, setName] = useState('');
  const [scopeSet, setScopeSet] = useState<Set<string>>(() => new Set());
  const [expiresIn, setExpiresIn] = useState<ExpiresPreset>('30d');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const scopes = useMemo(() => Array.from(scopeSet), [scopeSet]);

  const toggleScope = useCallback((slug: string) => {
    setScopeSet((prev) => {
      const next = new Set(prev);
      if (next.has(slug)) {
        next.delete(slug);
      } else {
        next.add(slug);
      }
      return next;
    });
  }, []);

  const onSubmit = useCallback(
    async (e: FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      setError(null);

      const trimmed = name.trim();
      if (!trimmed) {
        setError('Give this token a memorable name.');
        return;
      }
      if (scopes.length === 0) {
        setError(
          'Pick at least one scope. A token with no scopes can’t do anything useful.',
        );
        return;
      }

      setSubmitting(true);
      try {
        const issued = await issueToken({
          name: trimmed,
          scopes,
          expires_in: expiresIn,
        });
        onIssued(issued);
      } catch (err) {
        const message =
          err instanceof ApiError
            ? `API error ${err.status}: ${err.statusText}`
            : err instanceof Error
              ? err.message
              : 'Failed to issue token';
        setError(message);
      } finally {
        setSubmitting(false);
      }
    },
    [expiresIn, name, onIssued, scopes],
  );

  return (
    <form
      onSubmit={onSubmit}
      className="flex flex-col gap-6"
      data-testid="new-token-form"
    >
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-token-name">Name</Label>
        <Input
          id="new-token-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
          maxLength={120}
          placeholder="github-actions, laptop-cli, …"
          data-testid="new-token-name"
        />
        <p className="font-sans text-xs text-fg-subtle">
          Pick a name future-you will recognise in the list.
        </p>
      </div>

      <fieldset className="flex flex-col gap-3">
        <legend className="flex items-center gap-2 font-display text-sm font-bold uppercase tracking-[0.06em] text-ink">
          <ShieldCheck
            className="h-4 w-4 text-emerald-deep"
            aria-hidden="true"
          />
          Scopes
        </legend>
        <p className="font-sans text-sm text-fg-muted">
          Pick the smallest set that lets your script work. Scopes are
          intersected with your own permissions at every request — a token
          can never do more than you can.
        </p>
        <ul className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {SCOPE_OPTIONS.map((opt) => {
            const checked = scopeSet.has(opt.slug);
            return (
              <li key={opt.slug}>
                <label
                  className={cn(
                    'flex cursor-pointer items-start gap-3 rounded-md border bg-paper-3 px-3 py-2.5',
                    'transition-colors duration-[160ms] ease-brand',
                    checked
                      ? 'border-lavender-deep bg-lavender-soft'
                      : 'border-border hover:border-border-strong',
                  )}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => toggleScope(opt.slug)}
                    data-testid={`scope-${opt.slug}`}
                    className="mt-0.5 h-4 w-4 cursor-pointer accent-emerald"
                  />
                  <span className="flex flex-1 flex-col gap-0.5">
                    <span className="flex items-center gap-2">
                      <strong className="font-display text-sm font-bold text-ink">
                        {opt.label}
                      </strong>
                      <Badge variant="lavender" className="font-mono">
                        {opt.slug}
                      </Badge>
                    </span>
                    <span className="font-sans text-xs text-fg-muted">
                      {opt.description}
                    </span>
                  </span>
                </label>
              </li>
            );
          })}
        </ul>
      </fieldset>

      <fieldset className="flex flex-col gap-3">
        <legend className="font-display text-sm font-bold uppercase tracking-[0.06em] text-ink">
          Expiration
        </legend>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {EXPIRY_PRESETS.map((p) => {
            const checked = expiresIn === p.value;
            return (
              <label
                key={p.value}
                className={cn(
                  'flex cursor-pointer items-start gap-3 rounded-md border bg-paper-3 px-3 py-2.5',
                  'transition-colors duration-[160ms] ease-brand',
                  checked
                    ? 'border-emerald bg-emerald-soft'
                    : 'border-border hover:border-border-strong',
                )}
              >
                <input
                  type="radio"
                  name="expires_in"
                  value={p.value}
                  checked={checked}
                  onChange={() => setExpiresIn(p.value)}
                  data-testid={`expiry-${p.value}`}
                  className="mt-0.5 h-4 w-4 cursor-pointer accent-emerald"
                />
                <span className="flex flex-1 flex-col gap-0.5">
                  <strong className="font-display text-sm font-bold text-ink">
                    {p.label}
                  </strong>
                  <span className="font-sans text-xs text-fg-muted">
                    {p.hint}
                  </span>
                </span>
              </label>
            );
          })}
        </div>
      </fieldset>

      {error && (
        <p
          role="alert"
          className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 font-sans text-sm text-danger"
          data-testid="new-token-error"
        >
          {error}
        </p>
      )}

      <div className="flex items-center justify-end border-t border-border pt-4">
        <Button
          type="submit"
          variant="emerald"
          disabled={submitting}
          data-testid="new-token-submit"
        >
          <Key className="h-4 w-4" aria-hidden="true" />
          {submitting ? 'Issuing…' : 'Generate token'}
        </Button>
      </div>
    </form>
  );
}
