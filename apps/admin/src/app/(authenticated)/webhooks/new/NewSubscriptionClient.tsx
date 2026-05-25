'use client';

/**
 * <NewSubscriptionClient> — form for creating a webhook subscription.
 *
 * Brand: Living-Systems (#432). The form lives inside a paper-2 Card.
 * The post-create state replaces the form with a "secret reveal" panel:
 * a recessed paper-3 surface holding the HMAC hex in Geist Mono, an
 * emerald "Copy" CTA, and a hard-stop warning on the danger-soft
 * surface — this is the one-time secret reveal the API contract
 * specifies.
 *
 * Flow:
 *
 *  1. Operator types a name, URL, and selects events.
 *  2. Submit POSTs to /api/v1/admin/webhooks.
 *  3. On success the API returns the subscription PLUS the raw secret
 *     (hex string). We render the secret prominently with a copy
 *     button — this is the only chance to grab it.
 *  4. After acknowledgement, a "Go to subscription" link routes to the
 *     detail page.
 *
 * The component keeps minimal state — there's no autosave, no draft
 * persistence. Webhook subscriptions are quick to fill in.
 */
import { useRouter } from 'next/navigation';
import { AlertTriangle, Check, Copy, KeyRound } from 'lucide-react';
import {
  useCallback,
  useState,
  type ChangeEvent,
  type FormEvent,
  type ReactElement,
} from 'react';
import { Card } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { createSubscription } from '../actions';
import { EventCatalog } from '../components/EventCatalog';
import type { SubscriptionWithSecret } from '../types';

interface FormState {
  name: string;
  url: string;
  events: Set<string>;
}

export function NewSubscriptionClient(): ReactElement {
  const router = useRouter();
  const [state, setState] = useState<FormState>({
    name: '',
    url: '',
    events: new Set(),
  });
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<SubscriptionWithSecret | null>(null);
  const [copied, setCopied] = useState(false);

  const handleChange = useCallback(
    (key: 'name' | 'url') => (ev: ChangeEvent<HTMLInputElement>) => {
      setState((prev) => ({ ...prev, [key]: ev.target.value }));
    },
    [],
  );

  const handleEventsChange = useCallback((next: ReadonlySet<string>) => {
    setState((prev) => ({ ...prev, events: new Set(next) }));
  }, []);

  const handleSubmit = useCallback(
    async (ev: FormEvent<HTMLFormElement>) => {
      ev.preventDefault();
      setError(null);
      setSubmitting(true);
      try {
        const sub = await createSubscription({
          name: state.name.trim(),
          url: state.url.trim(),
          events: Array.from(state.events),
        });
        setCreated(sub);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setSubmitting(false);
      }
    },
    [state],
  );

  const handleCopy = useCallback(async () => {
    if (!created) return;
    try {
      await navigator.clipboard?.writeText(created.secret);
      setCopied(true);
    } catch {
      // Older browsers without async clipboard fall through silently —
      // the secret is in the textarea, the operator can select-all.
    }
  }, [created]);

  if (created) {
    return (
      <Card
        data-testid="webhook-created-panel"
        className="overflow-hidden"
      >
        <div className="border-b border-border bg-paper-2 px-6 py-4">
          <div className="flex items-center gap-2">
            <KeyRound
              className="h-[18px] w-[18px] text-emerald-deep"
              aria-hidden="true"
            />
            <h2 className="font-display text-lg font-bold tracking-tight text-ink">
              Subscription <em className="font-serif italic font-normal text-emerald-deep text-[1.05em] tracking-[-0.01em]">
                created
              </em>
            </h2>
          </div>
        </div>
        <div className="px-6 py-5">
          {/* One-time secret reveal warning — danger-soft on a deliberate
              alert tone, but using a calm warning glyph so the surface
              still feels measured. */}
          <div
            role="alert"
            className="mb-4 flex items-start gap-3 rounded-md border border-warning/40 bg-warning-soft px-4 py-3"
          >
            <AlertTriangle
              className="mt-[2px] h-4 w-4 flex-shrink-0 text-warning"
              aria-hidden="true"
            />
            <div className="font-sans text-sm text-ink-soft">
              <strong className="font-bold">
                Copy the signing secret now.
              </strong>{' '}
              We do not show it again — subscribers need this to verify
              our HMAC signature.
            </div>
          </div>

          {/* Recessed paper-3 surface holding the secret in Geist Mono.
              The textarea remains for fallback select-all on browsers
              without the async clipboard API. */}
          <div className="flex flex-col gap-2">
            <Label
              htmlFor="created-secret-field"
              className="font-display text-xs font-bold uppercase tracking-[0.08em] text-fg-subtle"
            >
              Signing secret
            </Label>
            <textarea
              id="created-secret-field"
              readOnly
              value={created.secret}
              rows={3}
              data-testid="created-secret"
              className="w-full resize-none rounded-md border border-border bg-paper-3 px-3 py-2 font-mono text-xs text-ink-soft selection:bg-emerald-soft focus-visible:outline-none focus-visible:border-emerald focus-visible:shadow-focus"
            />
          </div>

          <div className="mt-5 flex items-center gap-2">
            <Button
              type="button"
              variant="emerald"
              onClick={() => void handleCopy()}
            >
              {copied ? (
                <>
                  <Check className="h-[14px] w-[14px]" aria-hidden="true" />
                  Copied
                </>
              ) : (
                <>
                  <Copy className="h-[14px] w-[14px]" aria-hidden="true" />
                  Copy to clipboard
                </>
              )}
            </Button>
            <Button
              type="button"
              variant="default"
              onClick={() =>
                router.push(`/webhooks/${encodeURIComponent(created.id)}`)
              }
            >
              Go to subscription
            </Button>
          </div>
        </div>
      </Card>
    );
  }

  return (
    <Card className="overflow-hidden">
      <div className="border-b border-border bg-paper-2 px-6 py-4">
        <h2 className="font-display text-sm font-bold uppercase tracking-[0.08em] text-fg-subtle">
          Configuration
        </h2>
      </div>
      <form
        onSubmit={(ev) => void handleSubmit(ev)}
        noValidate
        className="flex flex-col gap-5 px-6 py-5"
      >
        {error ? (
          <div
            role="alert"
            className="rounded-md border border-danger/30 bg-danger-soft px-4 py-3 font-sans text-sm text-danger"
          >
            {error}
          </div>
        ) : null}
        <div className="flex flex-col gap-[6px]">
          <Label htmlFor="name">Name</Label>
          <Input
            id="name"
            type="text"
            value={state.name}
            onChange={handleChange('name')}
            required
            maxLength={200}
            aria-describedby="name-hint"
          />
          <small id="name-hint" className="font-sans text-xs text-fg-subtle">
            A human-readable label. Shown in the list and in audit log
            entries.
          </small>
        </div>
        <div className="flex flex-col gap-[6px]">
          <Label htmlFor="url">Endpoint URL</Label>
          <Input
            id="url"
            type="url"
            value={state.url}
            onChange={handleChange('url')}
            required
            placeholder="https://example.com/webhooks/gonext"
            aria-describedby="url-hint"
          />
          <small id="url-hint" className="font-sans text-xs text-fg-subtle">
            We send a POST to this URL on every matching event. The
            worker enforces HTTPS in production.
          </small>
        </div>
        <EventCatalog
          value={state.events}
          onChange={handleEventsChange}
        />
        <div>
          <Button type="submit" variant="emerald" disabled={submitting}>
            {submitting ? 'Creating…' : 'Create subscription'}
          </Button>
        </div>
      </form>
    </Card>
  );
}
