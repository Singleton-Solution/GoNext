'use client';

/**
 * NewTokenForm — the form on /settings/tokens/new.
 *
 * Fields:
 *  - name: free-text, required.
 *  - scopes: multi-select against SCOPE_OPTIONS. At least one is required.
 *  - expires_in: radio group of the four documented presets.
 *
 * On submit:
 *  - Validates locally so the API doesn't have to bounce trivial inputs.
 *  - POSTs to issueToken.
 *  - On success, hands the IssuedTokenView to the parent via onIssued
 *    which is responsible for showing TokenReveal.
 *  - On failure, surfaces the message inline.
 */

import type { FormEvent, ReactElement } from 'react';
import { useCallback, useMemo, useState } from 'react';
import { ApiError } from '../../../api-client';
import { issueToken } from '../api';
import type { ExpiresPreset, IssuedTokenView } from '../types';
import { SCOPE_OPTIONS } from '../types';

export interface NewTokenFormProps {
  /**
   * Called with the issued token on success. Parent shows TokenReveal.
   */
  onIssued: (token: IssuedTokenView) => void;
}

const EXPIRY_PRESETS: ReadonlyArray<{ value: ExpiresPreset; label: string; hint: string }> = [
  { value: '30d', label: '30 days', hint: 'Short-lived; recommended for one-off scripts.' },
  { value: '90d', label: '90 days', hint: 'Quarterly rotation; good for CI tokens reviewed at each release.' },
  { value: '1y', label: '1 year', hint: 'Long-lived; pair with calendar reminders.' },
  { value: 'never', label: 'No expiry', hint: 'Never expires. Only use for tokens you actively monitor.' },
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
        setError('Pick at least one scope. A token with no scopes can’t do anything useful.');
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
    <form onSubmit={onSubmit} className="new-token-form" data-testid="new-token-form">
      <label className="new-token-form__field">
        <span>Name</span>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
          maxLength={120}
          placeholder="github-actions, laptop-cli, …"
          data-testid="new-token-name"
        />
      </label>

      <fieldset className="new-token-form__field new-token-form__scopes">
        <legend>Scopes</legend>
        <p className="muted">
          Pick the smallest set that lets your script work. Scopes are
          intersected with your own permissions at every request — a token
          can never do more than you can.
        </p>
        <ul>
          {SCOPE_OPTIONS.map((opt) => (
            <li key={opt.slug}>
              <label>
                <input
                  type="checkbox"
                  checked={scopeSet.has(opt.slug)}
                  onChange={() => toggleScope(opt.slug)}
                  data-testid={`scope-${opt.slug}`}
                />
                <strong>{opt.label}</strong>
                <span className="muted"> — {opt.description}</span>
              </label>
            </li>
          ))}
        </ul>
      </fieldset>

      <fieldset className="new-token-form__field new-token-form__expiry">
        <legend>Expiration</legend>
        {EXPIRY_PRESETS.map((p) => (
          <label key={p.value}>
            <input
              type="radio"
              name="expires_in"
              value={p.value}
              checked={expiresIn === p.value}
              onChange={() => setExpiresIn(p.value)}
              data-testid={`expiry-${p.value}`}
            />
            <strong>{p.label}</strong>
            <span className="muted"> — {p.hint}</span>
          </label>
        ))}
      </fieldset>

      {error && (
        <p role="alert" className="new-token-form__error" data-testid="new-token-error">
          {error}
        </p>
      )}

      <div className="new-token-form__actions">
        <button
          type="submit"
          disabled={submitting}
          className="btn btn-primary"
          data-testid="new-token-submit"
        >
          {submitting ? 'Issuing…' : 'Generate token'}
        </button>
      </div>
    </form>
  );
}
