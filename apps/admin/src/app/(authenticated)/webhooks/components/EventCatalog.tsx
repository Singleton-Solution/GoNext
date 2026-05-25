'use client';

/**
 * <EventCatalog> renders a multi-select over the registered webhook
 * event names. The catalog is fetched lazily on mount; the component
 * keeps an internal "selected" set in sync with the parent via
 * `value` + `onChange`.
 *
 * Brand: Living-Systems (#432). Each event is a clickable label-row;
 * selected entries pick up a lavender accent (the data-viz / deliveries
 * tone from pulse.html). The event name renders in Geist Mono. The
 * empty-selection warning sits on warning-soft so an operator can't
 * miss it.
 *
 * Rendering choices:
 *
 *  - Each event is a checkbox with the event name in Geist Mono and the
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
import { Button } from '@/components/ui/button';
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
      <div
        aria-busy="true"
        className="rounded-md border border-dashed border-border bg-paper-3 px-4 py-3 font-sans text-xs text-fg-muted"
      >
        Loading event catalog…
      </div>
    );
  }
  if (error && !catalog) {
    return (
      <div
        role="alert"
        className="flex items-center justify-between gap-3 rounded-md border border-danger/30 bg-danger-soft px-4 py-3 font-sans text-xs text-danger"
      >
        <span>Couldn&apos;t load events: {error.message}</span>
        <Button
          type="button"
          variant="default"
          size="sm"
          onClick={() => void load()}
        >
          Retry
        </Button>
      </div>
    );
  }
  return (
    <fieldset className="rounded-md border border-border bg-paper p-4">
      <legend className="px-1 font-display text-xs font-bold uppercase tracking-[0.08em] text-fg-subtle">
        Events
      </legend>
      {value.size === 0 ? (
        <div
          role="status"
          className="mb-3 rounded-md border border-warning/30 bg-warning-soft px-3 py-2 font-sans text-xs text-warning"
        >
          No events selected — the worker will not match any traffic for
          this subscription.
        </div>
      ) : null}
      <ul className="m-0 flex flex-col gap-1 p-0">
        {visible.map((ev) => {
          const checked = value.has(ev.name);
          return (
            <li key={ev.name}>
              <label
                className={
                  checked
                    ? 'flex cursor-pointer items-start gap-3 rounded-md border border-lavender/40 bg-lavender-soft px-3 py-2 transition-colors'
                    : 'flex cursor-pointer items-start gap-3 rounded-md border border-transparent bg-paper px-3 py-2 transition-colors hover:bg-paper-2 hover:border-border-subtle'
                }
              >
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={handleToggle(ev.name)}
                  aria-label={`Subscribe to ${ev.name}`}
                  className="mt-[2px] h-4 w-4 flex-shrink-0 cursor-pointer accent-lavender-deep"
                />
                <span className="block">
                  <code
                    className={
                      checked
                        ? 'font-mono text-xs font-semibold text-lavender-deep'
                        : 'font-mono text-xs font-semibold text-ink'
                    }
                  >
                    {ev.name}
                  </code>
                  <span className="block font-sans text-xs text-fg-muted">
                    {ev.description}
                  </span>
                </span>
              </label>
            </li>
          );
        })}
      </ul>
    </fieldset>
  );
}
