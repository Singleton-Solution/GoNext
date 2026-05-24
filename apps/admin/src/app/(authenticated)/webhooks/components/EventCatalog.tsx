'use client';

/**
 * <EventCatalog> renders a multi-select over the registered webhook
 * event names. The catalog is fetched lazily on mount; the component
 * keeps an internal "selected" set in sync with the parent via
 * `value` + `onChange`.
 *
 * Rendering choices:
 *
 *  - Each event is a checkbox with the event name in <code> and the
 *    operator-facing description as a hint. Checkboxes (rather than a
 *    multi-select <select>) so the list stays readable when more than
 *    a handful of events exist.
 *
 *  - When the catalog fetch fails, we render a friendly error with a
 *    retry button. The form remains usable — the operator can save a
 *    subscription with the events they had selected, the validation
 *    is server-side.
 *
 *  - Empty selection is allowed by the API (it means "the worker
 *    matches nothing", which the UI also flags with an inline
 *    warning). We surface that warning here rather than at form
 *    submit so it appears closer to the field.
 */
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ChangeEvent,
  type ReactElement,
} from 'react';
import { listEventCatalog } from '../actions';
import type { EventDescriptor } from '../types';

export interface EventCatalogProps {
  value: ReadonlySet<string>;
  onChange: (next: ReadonlySet<string>) => void;
  /** Hide the reserved `webhook.test` event from the list — it is
   *  not meant to be a subscription target on its own. */
  hideReserved?: boolean;
}

const RESERVED_EVENTS = new Set(['webhook.test']);

export function EventCatalog({
  value,
  onChange,
  hideReserved = true,
}: EventCatalogProps): ReactElement {
  const [catalog, setCatalog] = useState<EventDescriptor[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await listEventCatalog();
      setCatalog(res.data);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const visible = useMemo<EventDescriptor[]>(() => {
    if (!catalog) return [];
    return hideReserved
      ? catalog.filter((e) => !RESERVED_EVENTS.has(e.name))
      : catalog;
  }, [catalog, hideReserved]);

  const handleToggle = useCallback(
    (name: string) => (ev: ChangeEvent<HTMLInputElement>) => {
      const next = new Set(value);
      if (ev.target.checked) {
        next.add(name);
      } else {
        next.delete(name);
      }
      onChange(next);
    },
    [onChange, value],
  );

  if (loading && !catalog) {
    return (
      <div aria-busy="true" className="muted">
        Loading event catalog…
      </div>
    );
  }
  if (error && !catalog) {
    return (
      <div role="alert" className="muted">
        Couldn&apos;t load events: {error.message}{' '}
        <button type="button" onClick={() => void load()}>
          Retry
        </button>
      </div>
    );
  }
  return (
    <fieldset
      style={{
        border: '1px solid var(--color-border, #ddd)',
        borderRadius: 'var(--radius, 4px)',
        padding: 12,
      }}
    >
      <legend>Events</legend>
      {value.size === 0 ? (
        <div role="status" className="muted" style={{ marginBottom: 8 }}>
          No events selected — the worker will not match any traffic
          for this subscription.
        </div>
      ) : null}
      <ul
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'flex',
          flexDirection: 'column',
          gap: 8,
        }}
      >
        {visible.map((ev) => (
          <li key={ev.name}>
            <label
              style={{
                display: 'flex',
                alignItems: 'flex-start',
                gap: 8,
                cursor: 'pointer',
              }}
            >
              <input
                type="checkbox"
                checked={value.has(ev.name)}
                onChange={handleToggle(ev.name)}
                aria-label={`Subscribe to ${ev.name}`}
              />
              <span style={{ display: 'block' }}>
                <code style={{ fontWeight: 600 }}>{ev.name}</code>
                <span className="muted" style={{ display: 'block', fontSize: 13 }}>
                  {ev.description}
                </span>
              </span>
            </label>
          </li>
        ))}
      </ul>
    </fieldset>
  );
}
