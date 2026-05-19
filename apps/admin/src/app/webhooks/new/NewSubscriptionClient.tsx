'use client';

/**
 * <NewSubscriptionClient> — form for creating a webhook subscription.
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
import {
  useCallback,
  useState,
  type ChangeEvent,
  type FormEvent,
  type ReactElement,
} from 'react';
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
      <div
        style={{
          padding: 16,
          border: '1px solid var(--color-border, #ddd)',
          borderRadius: 4,
        }}
      >
        <h2 style={{ marginTop: 0 }}>Subscription created</h2>
        <div role="alert" style={{ marginBottom: 16 }}>
          <strong>Copy the signing secret now.</strong> We do not show it
          again — subscribers need this to verify our signature.
        </div>
        <label style={{ display: 'block', marginBottom: 8 }}>
          Signing secret
          <textarea
            readOnly
            value={created.secret}
            rows={2}
            data-testid="created-secret"
            style={{ width: '100%', fontFamily: 'monospace', marginTop: 4 }}
          />
        </label>
        <button type="button" onClick={() => void handleCopy()}>
          {copied ? 'Copied' : 'Copy to clipboard'}
        </button>{' '}
        <button
          type="button"
          onClick={() => router.push(`/webhooks/${encodeURIComponent(created.id)}`)}
        >
          Go to subscription
        </button>
      </div>
    );
  }

  return (
    <form onSubmit={(ev) => void handleSubmit(ev)} noValidate>
      {error ? (
        <div role="alert" style={{ color: 'var(--color-danger, #a00)', marginBottom: 8 }}>
          {error}
        </div>
      ) : null}
      <label style={{ display: 'block', marginBottom: 12 }}>
        Name
        <input
          type="text"
          value={state.name}
          onChange={handleChange('name')}
          required
          maxLength={200}
          style={{ display: 'block', width: '100%', marginTop: 4 }}
          aria-describedby="name-hint"
        />
        <small id="name-hint" className="muted">
          A human-readable label. Shown in the list and in audit log entries.
        </small>
      </label>
      <label style={{ display: 'block', marginBottom: 12 }}>
        Endpoint URL
        <input
          type="url"
          value={state.url}
          onChange={handleChange('url')}
          required
          placeholder="https://example.com/webhooks/gonext"
          style={{ display: 'block', width: '100%', marginTop: 4 }}
          aria-describedby="url-hint"
        />
        <small id="url-hint" className="muted">
          We send a POST to this URL on every matching event. The
          worker enforces HTTPS in production.
        </small>
      </label>
      <div style={{ marginBottom: 16 }}>
        <EventCatalog
          value={state.events}
          onChange={handleEventsChange}
        />
      </div>
      <button type="submit" disabled={submitting}>
        {submitting ? 'Creating…' : 'Create subscription'}
      </button>
    </form>
  );
}
