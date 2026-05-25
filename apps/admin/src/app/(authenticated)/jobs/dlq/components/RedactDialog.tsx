'use client';

/**
 * RedactDialog — modal for choosing which payload fields to mask on
 * an archived task.
 *
 * Brand: Living-Systems (#432). Cream paper-2 surface with sh-lg float,
 * Archivo title, Geist body, mono field names on paper-3 chips. The
 * confirm button lands on emerald (the affirmative tone in the brand
 * palette) — operators are applying a positive mask, not destroying
 * data, so this is the right colour.
 *
 * The dialog renders a checkbox per top-level field in the payload.
 * The parent passes the parsed payload-field list; we don't reach back
 * into the payload preview to derive the list, because the preview may
 * be truncated.
 *
 * Why a custom dialog (not shadcn Dialog): the existing tests target
 * data-testid hooks on a non-portaled root. Keeping the implementation
 * local preserves the test contract while letting us swap in the
 * design-system primitives for the surface itself.
 *
 * Keyboard model:
 *  - Esc closes the dialog without saving.
 *  - Tab cycles through checkboxes, then Cancel, then Apply.
 *  - Initial focus lands on the first checkbox so the user can start
 *    selecting immediately.
 */
import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type ChangeEvent,
  type KeyboardEvent,
  type ReactElement,
} from 'react';
import { Button } from '@/components/ui/button';

export interface RedactDialogProps {
  /** Whether the dialog is mounted/visible. */
  open: boolean;
  /** Top-level field names available for redaction. */
  fields: string[];
  /** Fields that are already redacted — pre-check these. */
  initiallySelected?: string[];
  /** Called with the chosen field set on Apply. */
  onApply: (selectedFields: string[]) => void;
  /** Called when the user dismisses (Esc, Cancel, backdrop click). */
  onCancel: () => void;
}

export function RedactDialog({
  open,
  fields,
  initiallySelected = [],
  onApply,
  onCancel,
}: RedactDialogProps): ReactElement | null {
  const titleId = useId();
  const [selected, setSelected] = useState<ReadonlySet<string>>(
    () => new Set(initiallySelected),
  );

  // Reset selection when the dialog transitions from closed → open so
  // a fresh task picks up its existing redactions. We intentionally do
  // NOT depend on the `initiallySelected` array identity — parents
  // typically pass a freshly-allocated slice every render, which
  // would re-set state on every render and loop. The string-join
  // sentinel pins us to the actual contents.
  const initialKey = initiallySelected.join('|');
  useEffect(() => {
    if (open) {
      setSelected(new Set(initiallySelected));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialKey]);

  // Esc to cancel.
  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onCancel();
      }
    },
    [onCancel],
  );

  const toggle = useCallback((field: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(field)) {
        next.delete(field);
      } else {
        next.add(field);
      }
      return next;
    });
  }, []);

  const firstCheckboxRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (open) {
      firstCheckboxRef.current?.focus();
    }
  }, [open]);

  if (!open) return null;

  const selectedArr = Array.from(selected);
  const applyDisabled = selectedArr.length === 0;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      onKeyDown={handleKeyDown}
      data-testid="redact-dialog"
      className="fixed inset-0 z-50 flex items-center justify-center bg-forest/55 backdrop-blur-[2px] p-4"
    >
      <div
        className="w-full max-w-[480px] rounded-xl border border-border bg-paper-2 shadow-lg"
      >
        <div className="border-b border-border px-6 py-5">
          <h2
            id={titleId}
            className="font-display text-lg font-bold tracking-tight text-ink"
          >
            Redact <em className="font-serif italic font-normal text-lavender-deep text-[1.05em] tracking-[-0.01em]">
              payload
            </em> fields
          </h2>
          <p className="mt-2 font-sans text-xs text-fg-muted">
            Selected fields will be masked as{' '}
            <code className="rounded-xs bg-paper-3 px-1 font-mono text-2xs text-ink-soft">
              ***REDACTED***
            </code>{' '}
            in the list and detail views. The original payload in Redis is
            untouched — replay still uses the unmasked value.
          </p>
        </div>
        <div className="px-6 py-5">
          {fields.length === 0 ? (
            <p
              data-testid="redact-dialog-empty"
              className="rounded-md border border-dashed border-border bg-paper-3 p-4 font-sans text-sm italic text-fg-muted"
            >
              No top-level fields available to redact. (Non-object payloads
              are wholesale-masked when any redaction is applied.)
            </p>
          ) : (
            <ul className="m-0 flex max-h-[260px] flex-col gap-1 overflow-y-auto p-0">
              {fields.map((field, idx) => {
                const id = `${titleId}-field-${idx}`;
                const checked = selected.has(field);
                return (
                  <li key={field}>
                    <label
                      htmlFor={id}
                      className={
                        checked
                          ? 'flex cursor-pointer items-center gap-3 rounded-md border border-lavender/30 bg-lavender-soft px-3 py-2 transition-colors'
                          : 'flex cursor-pointer items-center gap-3 rounded-md border border-transparent bg-paper px-3 py-2 transition-colors hover:bg-paper-3 hover:border-border'
                      }
                    >
                      <input
                        ref={idx === 0 ? firstCheckboxRef : undefined}
                        id={id}
                        type="checkbox"
                        checked={checked}
                        onChange={(_e: ChangeEvent<HTMLInputElement>) => {
                          toggle(field);
                        }}
                        data-testid={`redact-field-${field}`}
                        className="h-4 w-4 cursor-pointer accent-lavender-deep"
                      />
                      <code className="font-mono text-xs text-ink">
                        {field}
                      </code>
                    </label>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
        <div className="flex items-center justify-end gap-2 border-t border-border bg-paper-2 px-6 py-4">
          <Button
            type="button"
            variant="ghost"
            onClick={onCancel}
            data-testid="redact-cancel"
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="emerald"
            onClick={() => onApply(selectedArr)}
            disabled={applyDisabled}
            data-testid="redact-apply"
          >
            Apply redaction
          </Button>
        </div>
      </div>
    </div>
  );
}
