'use client';

/**
 * Redirect rule form with inline regex playground.
 *
 * Used by both /redirects/new (empty initial values) and
 * /redirects/[id] (hydrated from a server-fetched row). The playground
 * panel runs the same regex evaluation the backend does, so operators
 * see exactly what the engine will do before they save.
 */
import { useCallback, useState, type FormEvent, type ReactElement } from 'react';
import { useRouter } from 'next/navigation';
import { createRedirect, testRegex, updateRedirect } from './actions';
import type { Redirect, RedirectInput, RegexTestResponse } from './types';

interface Props {
  initial?: Redirect;
}

const STATUSES = [301, 302, 307, 308] as const;

export function RedirectForm({ initial }: Props): ReactElement {
  const router = useRouter();
  const [source, setSource] = useState(initial?.source_path ?? '');
  const [destination, setDestination] = useState(initial?.destination_path ?? '');
  const [status, setStatus] = useState<number>(initial?.status ?? 301);
  const [isRegex, setIsRegex] = useState<boolean>(initial?.is_regex ?? false);
  const [sample, setSample] = useState('');
  const [testResult, setTestResult] = useState<RegexTestResponse | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = useCallback(
    async (e: FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      setSubmitting(true);
      setError(null);
      const input: RedirectInput = {
        source_path: source.trim(),
        destination_path: destination.trim(),
        status,
        is_regex: isRegex,
      };
      try {
        if (initial) {
          await updateRedirect(initial.id, input);
        } else {
          await createRedirect(input);
        }
        router.push('/redirects');
        router.refresh();
      } catch (err) {
        setError(err instanceof Error ? err.message : 'save failed');
      } finally {
        setSubmitting(false);
      }
    },
    [destination, initial, isRegex, router, source, status],
  );

  const onTest = useCallback(async () => {
    setTestResult(null);
    try {
      const result = await testRegex({
        pattern: source,
        destination,
        sample_path: sample,
      });
      setTestResult(result);
    } catch (err) {
      setTestResult({
        compiles: false,
        matches: false,
        error: err instanceof Error ? err.message : 'test failed',
      });
    }
  }, [destination, sample, source]);

  return (
    <form onSubmit={onSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {error && (
        <div role="alert" style={{ padding: 8, background: '#fee2e2', color: '#7f1d1d' }}>
          {error}
        </div>
      )}

      <label>
        Source path
        <input
          type="text"
          value={source}
          onChange={(e) => setSource(e.target.value)}
          placeholder={isRegex ? '^/blog/(.+)$' : '/old-page'}
          required
          style={{ width: '100%', padding: 8 }}
        />
        <small className="muted">
          {isRegex
            ? 'Regular expression. Capture groups are available in the destination via $1, $2…'
            : 'Exact path the visitor hits. Include the leading slash.'}
        </small>
      </label>

      <label>
        Destination
        <input
          type="text"
          value={destination}
          onChange={(e) => setDestination(e.target.value)}
          placeholder={isRegex ? '/posts/$1' : '/new-page'}
          required
          style={{ width: '100%', padding: 8 }}
        />
      </label>

      <div style={{ display: 'flex', gap: 24, alignItems: 'center' }}>
        <label>
          HTTP status
          <select
            value={status}
            onChange={(e) => setStatus(Number(e.target.value))}
            style={{ padding: 8, marginLeft: 8 }}
          >
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {s}
                {s === 301 ? ' Moved Permanently' : ''}
                {s === 302 ? ' Found' : ''}
                {s === 307 ? ' Temporary Redirect' : ''}
                {s === 308 ? ' Permanent Redirect' : ''}
              </option>
            ))}
          </select>
        </label>
        <label>
          <input
            type="checkbox"
            checked={isRegex}
            onChange={(e) => {
              setIsRegex(e.target.checked);
              setTestResult(null);
            }}
          />
          Treat source as a regular expression
        </label>
      </div>

      {isRegex && (
        <fieldset style={{ padding: 12, border: '1px solid var(--color-border)' }}>
          <legend>Regex playground</legend>
          <p className="muted" style={{ marginTop: 0 }}>
            Type a sample request path; we will show whether your pattern matches
            and what the resolved destination would be.
          </p>
          <input
            type="text"
            value={sample}
            onChange={(e) => setSample(e.target.value)}
            placeholder="/blog/hello-world"
            aria-label="Sample request path"
            style={{ width: '100%', padding: 8 }}
          />
          <button
            type="button"
            onClick={onTest}
            disabled={!source || !sample}
            style={{ marginTop: 8 }}
          >
            Test pattern
          </button>
          {testResult && (
            <pre
              aria-live="polite"
              style={{
                marginTop: 8,
                padding: 8,
                background: '#f3f4f6',
                borderRadius: 'var(--radius)',
                fontSize: 12,
                whiteSpace: 'pre-wrap',
              }}
            >
              {!testResult.compiles
                ? `Pattern did not compile: ${testResult.error ?? 'unknown error'}`
                : !testResult.matches
                  ? 'Pattern compiled, but does not match the sample.'
                  : `Match!
Captures: ${(testResult.captures ?? []).map((c, i) => `$${i + 1}=${c}`).join('  ') || '(none)'}
Destination: ${testResult.destination ?? '(none)'}`}
            </pre>
          )}
        </fieldset>
      )}

      <div style={{ display: 'flex', gap: 8 }}>
        <button type="submit" disabled={submitting} className="primary-action">
          {submitting ? 'Saving…' : initial ? 'Save changes' : 'Create redirect'}
        </button>
        <button type="button" onClick={() => router.push('/redirects')}>
          Cancel
        </button>
      </div>
    </form>
  );
}
