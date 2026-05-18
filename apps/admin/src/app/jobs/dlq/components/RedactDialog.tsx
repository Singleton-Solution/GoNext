'use client';

/**
 * RedactDialog — modal for choosing which payload fields to mask on
 * an archived task.
 *
 * The dialog renders a checkbox per top-level field in the payload.
 * The parent passes the parsed payload-field list; we don't reach back
 * into the payload preview to derive the list, because the preview may
 * be truncated.
 *
 * Why a custom dialog and not the design-system primitive: docs/05
 * §2.3 calls for a Dialog component but that primitive hasn't shipped
 * yet (issue #34). Keeping this local makes the eventual swap a
 * mechanical replace rather than an API tweak.
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
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.4)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1000,
      }}
    >
      <div
        style={{
          background: 'var(--color-surface)',
          border: '1px solid var(--color-border)',
          borderRadius: 'var(--radius)',
          padding: 'var(--space-4)',
          minWidth: 360,
          maxWidth: 480,
          width: '90%',
        }}
      >
        <h2 id={titleId} style={{ marginTop: 0 }}>
          Redact payload fields
        </h2>
        <p className="muted" style={{ fontSize: 13 }}>
          Selected fields will be masked as <code>***REDACTED***</code> in
          the list and detail views. The original payload in Redis is
          untouched — replay still uses the unmasked value.
        </p>
        {fields.length === 0 ? (
          <p
            className="muted"
            data-testid="redact-dialog-empty"
            style={{ fontStyle: 'italic' }}
          >
            No top-level fields available to redact. (Non-object payloads
            are wholesale-masked when any redaction is applied.)
          </p>
        ) : (
          <ul
            style={{
              listStyle: 'none',
              padding: 0,
              margin: 'var(--space-3) 0',
              maxHeight: 240,
              overflowY: 'auto',
            }}
          >
            {fields.map((field, idx) => {
              const id = `${titleId}-field-${idx}`;
              const checked = selected.has(field);
              return (
                <li key={field} style={{ padding: '4px 0' }}>
                  <label htmlFor={id} style={{ cursor: 'pointer' }}>
                    <input
                      ref={idx === 0 ? firstCheckboxRef : undefined}
                      id={id}
                      type="checkbox"
                      checked={checked}
                      onChange={(_e: ChangeEvent<HTMLInputElement>) => {
                        toggle(field);
                      }}
                      data-testid={`redact-field-${field}`}
                      style={{ marginRight: 8 }}
                    />
                    <code>{field}</code>
                  </label>
                </li>
              );
            })}
          </ul>
        )}
        <div
          style={{
            display: 'flex',
            gap: 'var(--space-2)',
            justifyContent: 'flex-end',
            marginTop: 'var(--space-3)',
          }}
        >
          <button
            type="button"
            onClick={onCancel}
            data-testid="redact-cancel"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => onApply(selectedArr)}
            disabled={applyDisabled}
            data-testid="redact-apply"
          >
            Apply redaction
          </button>
        </div>
      </div>
    </div>
  );
}
