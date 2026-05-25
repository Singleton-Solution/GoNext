'use client';

/**
 * Redirect rule form — Living-Systems brand.
 *
 * Wears the cream-paper card on the page surface, mono inputs for the
 * source / destination paths, a Radix Select for the HTTP status code,
 * a brand Switch for the regex toggle, and a paper-3 well that hosts
 * the inline regex playground. The playground exposes:
 *
 *   - The same `testRegex()` server call as before — operators see
 *     exactly what the backend engine will do.
 *   - A sample list of three "paper-3" rows you can click to populate
 *     the input.
 *   - Emerald-bright `<mark>` highlights on matched capture groups,
 *     so the cause-and-effect is visible at a glance.
 *
 * Used by both /redirects/new (empty initial values) and
 * /redirects/[id] (hydrated from a server-fetched row).
 */
import { useCallback, useMemo, useState, type FormEvent, type ReactElement } from 'react';
import { useRouter } from 'next/navigation';
import { Sparkles, TerminalSquare } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { createRedirect, testRegex, updateRedirect } from './actions';
import type { Redirect, RedirectInput, RegexTestResponse } from './types';

interface Props {
  initial?: Redirect;
}

const STATUSES = [
  { value: 301, label: '301 · Moved Permanently' },
  { value: 302, label: '302 · Found' },
  { value: 307, label: '307 · Temporary Redirect' },
  { value: 308, label: '308 · Permanent Redirect' },
] as const;

// Suggested sample paths surfaced as paper-3 chips inside the regex
// playground. Picking a chip populates the sample field — small UX
// boost for operators who haven't memorised a representative URL yet.
const SAMPLE_PATHS = [
  '/blog/hello-world',
  '/docs/v1/quickstart',
  '/changelog/2025/06',
];

/**
 * Render the captured groups against the original sample so the
 * match locations get emerald-soft highlights. We use a simple
 * left-to-right walk because the captures arrive in order.
 */
function HighlightedSample({
  sample,
  captures,
}: {
  sample: string;
  captures: string[];
}): ReactElement {
  const segments = useMemo(() => {
    if (captures.length === 0) {
      return [{ text: sample, highlight: false }];
    }
    const parts: { text: string; highlight: boolean }[] = [];
    let cursor = 0;
    for (const cap of captures) {
      if (!cap) continue;
      const idx = sample.indexOf(cap, cursor);
      if (idx === -1) continue;
      if (idx > cursor) parts.push({ text: sample.slice(cursor, idx), highlight: false });
      parts.push({ text: cap, highlight: true });
      cursor = idx + cap.length;
    }
    if (cursor < sample.length) parts.push({ text: sample.slice(cursor), highlight: false });
    return parts;
  }, [sample, captures]);
  return (
    <code className="font-mono text-xs text-ink" data-testid="regex-match-highlights">
      {segments.map((seg, i) =>
        seg.highlight ? (
          <mark
            key={i}
            className="rounded-sm bg-emerald-soft px-0.5 text-emerald-deep"
          >
            {seg.text}
          </mark>
        ) : (
          <span key={i}>{seg.text}</span>
        ),
      )}
    </code>
  );
}

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
    <form onSubmit={onSubmit} className="flex flex-col gap-5" data-testid="redirect-form">
      {error && (
        <div role="alert" className="rounded-md border border-danger/40 bg-danger-soft px-3 py-2 text-sm text-danger">
          {error}
        </div>
      )}

      <div className="card flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="redirect-source" className="font-display text-xs font-medium uppercase tracking-wide text-fg-subtle">
            Source path
          </Label>
          <Input
            id="redirect-source"
            type="text"
            value={source}
            onChange={(e) => setSource(e.target.value)}
            placeholder={isRegex ? '^/blog/(.+)$' : '/old-page'}
            required
            data-testid="source-input"
            className="font-mono"
          />
          <p className="text-xs text-fg-muted">
            {isRegex
              ? 'Regular expression. Capture groups are available in the destination via $1, $2…'
              : 'Exact path the visitor hits. Include the leading slash.'}
          </p>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="redirect-destination" className="font-display text-xs font-medium uppercase tracking-wide text-fg-subtle">
            Destination
          </Label>
          <Input
            id="redirect-destination"
            type="text"
            value={destination}
            onChange={(e) => setDestination(e.target.value)}
            placeholder={isRegex ? '/posts/$1' : '/new-page'}
            required
            data-testid="destination-input"
            className="font-mono text-emerald-deep"
          />
        </div>

        <div className="flex flex-wrap items-end gap-6">
          <div className="flex min-w-[14rem] flex-col gap-1.5">
            <Label className="font-display text-xs font-medium uppercase tracking-wide text-fg-subtle" id="status-label">
              HTTP status
            </Label>
            <Select value={String(status)} onValueChange={(v) => setStatus(Number(v))}>
              <SelectTrigger aria-labelledby="status-label" data-testid="status-select">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATUSES.map((s) => (
                  <SelectItem key={s.value} value={String(s.value)}>
                    {s.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-center gap-3 pb-1">
            <Switch
              id="redirect-regex"
              checked={isRegex}
              onCheckedChange={(checked) => {
                setIsRegex(checked);
                setTestResult(null);
              }}
              data-testid="regex-switch"
            />
            <Label htmlFor="redirect-regex" className="text-sm text-ink">
              Treat source as a regular expression
            </Label>
          </div>
        </div>
      </div>

      {isRegex && (
        <fieldset
          className="rounded-lg border border-border bg-paper-3 p-4"
          data-testid="regex-playground"
        >
          <legend className="flex items-center gap-2 rounded-sm bg-paper-2 px-2 py-0.5 font-display text-xs font-medium uppercase tracking-wide text-emerald-deep">
            <TerminalSquare aria-hidden="true" size={14} />
            Regex playground
          </legend>
          <p className="mt-1 text-sm text-fg-muted">
            Try a sample request path — we will show whether your pattern
            matches and what the resolved destination would be.
          </p>

          <div className="mt-3 flex flex-col gap-2">
            <Label htmlFor="regex-sample" className="text-xs text-fg-subtle">
              Sample request path
            </Label>
            <Input
              id="regex-sample"
              type="text"
              value={sample}
              onChange={(e) => setSample(e.target.value)}
              placeholder="/blog/hello-world"
              data-testid="sample-input"
              className="bg-paper font-mono"
            />
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs text-fg-subtle">Try:</span>
              {SAMPLE_PATHS.map((path) => (
                <button
                  key={path}
                  type="button"
                  onClick={() => setSample(path)}
                  data-testid={`sample-chip-${path}`}
                  className="rounded-sm border border-border-subtle bg-paper px-2 py-1 font-mono text-xs text-ink-soft transition-colors duration-[160ms] ease-brand hover:border-emerald hover:text-emerald-deep"
                >
                  {path}
                </button>
              ))}
            </div>
          </div>

          <div className="mt-3 flex items-center gap-3">
            <Button
              type="button"
              variant="default"
              size="sm"
              onClick={onTest}
              disabled={!source || !sample}
              data-testid="test-regex"
            >
              <Sparkles aria-hidden="true" size={14} />
              Test pattern
            </Button>
            {testResult?.matches && (
              <span className="text-xs text-emerald-deep">Match!</span>
            )}
          </div>

          {testResult && (
            <div
              aria-live="polite"
              role="status"
              className="mt-3 flex flex-col gap-2 rounded-md border border-border bg-paper px-3 py-2 text-sm"
              data-testid="regex-result"
            >
              {!testResult.compiles ? (
                <p className="text-danger">
                  Pattern did not compile: {testResult.error ?? 'unknown error'}
                </p>
              ) : !testResult.matches ? (
                <p className="text-fg-muted">
                  Pattern compiled, but does not match the sample.
                </p>
              ) : (
                <>
                  <div className="flex flex-col gap-1">
                    <span className="text-xs text-fg-subtle">Sample</span>
                    <HighlightedSample
                      sample={sample}
                      captures={testResult.captures ?? []}
                    />
                  </div>
                  <div className="flex flex-col gap-1">
                    <span className="text-xs text-fg-subtle">Captures</span>
                    <code className="font-mono text-xs text-ink">
                      {(testResult.captures ?? []).length === 0
                        ? '(none)'
                        : (testResult.captures ?? [])
                            .map((c, i) => `$${i + 1}=${c}`)
                            .join('  ')}
                    </code>
                  </div>
                  <div className="flex flex-col gap-1">
                    <span className="text-xs text-fg-subtle">Resolved destination</span>
                    <code className="font-mono text-xs text-emerald-deep">
                      {testResult.destination ?? '(none)'}
                    </code>
                  </div>
                </>
              )}
            </div>
          )}
        </fieldset>
      )}

      <div className="flex items-center gap-2">
        <Button
          type="submit"
          variant="emerald"
          size="default"
          disabled={submitting}
          data-testid="submit"
        >
          {submitting ? 'Saving…' : initial ? 'Save changes' : 'Create redirect'}
        </Button>
        <Button type="button" variant="ghost" onClick={() => router.push('/redirects')}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
