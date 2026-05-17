'use client';

/**
 * SettingsForm — generic, schema-driven form for any settings group.
 *
 * Consumes a `Setting[]` schema plus an initial values map and renders one
 * labelled control per entry. On submit it:
 *
 *  1. Validates each field locally (required + type-specific rules — URLs are
 *     parsed with `URL`, numbers are checked with `Number.isFinite`).
 *  2. On failure: stops, paints per-field error messages.
 *  3. On success: optimistically updates local state, fires PATCH, and shows
 *     a transient toast (success or error). Toasts auto-dismiss after 4s.
 *
 * The component is intentionally I/O-free — the PATCH call is injected so
 * tests can substitute a stub without touching `fetch`. Production callers
 * import `patchSettings` from `./api` and pass it in.
 *
 * Keep this file generic. Group-specific business rules live in the page
 * components that compose the schema array.
 */
import { useEffect, useRef, useState, type FormEvent, type ReactElement } from 'react';
import type { Setting, SettingsValues } from './types';

const TOAST_DISMISS_MS = 4_000;

export interface SettingsFormProps {
  /** Field descriptors. Order is preserved in the rendered form. */
  schema: readonly Setting[];
  /** Pre-fill values keyed by `Setting.key`. Missing keys render empty. */
  initialValues: SettingsValues;
  /** Submission strategy. Defaults to the shared `patchSettings` helper. */
  onSubmit: (patch: SettingsValues) => Promise<SettingsValues | void>;
  /** Optional banner shown above the form (e.g. "API not available"). */
  banner?: string;
  /**
   * Optional render prop for the `permalinks` page to display a preview
   * derived from current field values. Receives the live values map.
   */
  renderExtras?: (values: SettingsValues) => ReactElement | null;
}

type FieldErrors = Record<string, string | undefined>;
type ToastState =
  | { kind: 'success'; message: string }
  | { kind: 'error'; message: string }
  | null;

/**
 * Coerce the value stored in state into a form-control-friendly string.
 * Booleans bypass this — they bind to `checked` on a `<input type="checkbox">`.
 */
function asInputValue(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number') return String(value);
  return '';
}

/**
 * Local validation. Mirrors what the registry will enforce server-side, but
 * runs synchronously so the user gets feedback before a round-trip.
 */
function validate(
  schema: readonly Setting[],
  values: SettingsValues,
): FieldErrors {
  const errors: FieldErrors = {};
  for (const field of schema) {
    const raw = values[field.key];

    if (field.required && (raw === '' || raw === undefined || raw === null)) {
      errors[field.key] = `${field.label} is required.`;
      continue;
    }

    if (raw === '' || raw === undefined || raw === null) continue;

    switch (field.type) {
      case 'url': {
        try {
          // `URL` throws on anything that isn't an absolute URL.
          // Both `http://localhost` and `https://example.com/foo` pass.
          // eslint-disable-next-line no-new
          new URL(String(raw));
        } catch {
          errors[field.key] = `${field.label} must be a valid URL.`;
        }
        break;
      }
      case 'number': {
        const n = typeof raw === 'number' ? raw : Number(raw);
        if (!Number.isFinite(n)) {
          errors[field.key] = `${field.label} must be a number.`;
        }
        break;
      }
      case 'select': {
        const allowed = (field.options ?? []).map((o) => o.value);
        if (!allowed.includes(String(raw))) {
          errors[field.key] = `${field.label} must be one of: ${allowed.join(', ')}.`;
        }
        break;
      }
      default:
        break;
    }
  }
  return errors;
}

/**
 * Build the patch payload sent to the API. Coerces inputs back to their
 * declared types (numbers as `number`, booleans as `boolean`) so the
 * registry stores them with the correct shape.
 */
function buildPatch(
  schema: readonly Setting[],
  values: SettingsValues,
): SettingsValues {
  const out: SettingsValues = {};
  for (const field of schema) {
    const raw = values[field.key];
    if (raw === undefined) continue;
    switch (field.type) {
      case 'number':
        out[field.key] = typeof raw === 'number' ? raw : Number(raw);
        break;
      case 'boolean':
        out[field.key] = Boolean(raw);
        break;
      default:
        out[field.key] = typeof raw === 'string' ? raw : String(raw);
        break;
    }
  }
  return out;
}

export function SettingsForm({
  schema,
  initialValues,
  onSubmit,
  banner,
  renderExtras,
}: SettingsFormProps): ReactElement {
  const [values, setValues] = useState<SettingsValues>(() => ({ ...initialValues }));
  const [errors, setErrors] = useState<FieldErrors>({});
  const [toast, setToast] = useState<ToastState>(null);
  const [submitting, setSubmitting] = useState(false);
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Dismiss any pending toast timer when the component unmounts so a late
  // setState doesn't leak after navigation.
  useEffect(() => {
    return () => {
      if (toastTimer.current) clearTimeout(toastTimer.current);
    };
  }, []);

  function showToast(next: ToastState): void {
    if (toastTimer.current) clearTimeout(toastTimer.current);
    setToast(next);
    if (next) {
      toastTimer.current = setTimeout(() => setToast(null), TOAST_DISMISS_MS);
    }
  }

  function updateField(key: string, value: unknown): void {
    setValues((prev) => ({ ...prev, [key]: value }));
    // Clear the per-field error as soon as the user edits the field — wait
    // until the next submit to re-validate.
    setErrors((prev) => {
      if (!prev[key]) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (submitting) return;

    const validation = validate(schema, values);
    setErrors(validation);
    if (Object.keys(validation).length > 0) {
      showToast({ kind: 'error', message: 'Please fix the highlighted fields.' });
      return;
    }

    const patch = buildPatch(schema, values);
    setSubmitting(true);
    try {
      await onSubmit(patch);
      showToast({ kind: 'success', message: 'Settings saved.' });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : 'Failed to save settings.';
      showToast({ kind: 'error', message });
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form className="settings-form" onSubmit={handleSubmit} noValidate>
      {banner ? (
        <p className="settings-form__banner" role="status">
          {banner}
        </p>
      ) : null}

      {schema.map((field) => (
        <Field
          key={field.key}
          field={field}
          value={values[field.key]}
          error={errors[field.key]}
          onChange={(v) => updateField(field.key, v)}
        />
      ))}

      {renderExtras ? renderExtras(values) : null}

      <div className="settings-form__actions">
        <button type="submit" className="btn-primary" disabled={submitting}>
          {submitting ? 'Saving…' : 'Save changes'}
        </button>
      </div>

      {toast ? (
        <div
          className={
            toast.kind === 'success'
              ? 'settings-form__toast settings-form__toast--success'
              : 'settings-form__toast settings-form__toast--error'
          }
          role={toast.kind === 'error' ? 'alert' : 'status'}
        >
          {toast.message}
        </div>
      ) : null}
    </form>
  );
}

interface FieldProps {
  field: Setting;
  value: unknown;
  error: string | undefined;
  onChange: (next: unknown) => void;
}

function Field({ field, value, error, onChange }: FieldProps): ReactElement {
  const inputId = `setting-${field.key.replace(/\./g, '-')}`;
  const errorId = `${inputId}-error`;

  return (
    <div className="form-field">
      <label htmlFor={inputId}>{field.label}</label>
      {renderControl(field, inputId, value, onChange, error ? errorId : undefined)}
      {field.help ? (
        <p className="form-field__help muted">{field.help}</p>
      ) : null}
      {error ? (
        <p id={errorId} className="form-field__error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}

function renderControl(
  field: Setting,
  inputId: string,
  value: unknown,
  onChange: (next: unknown) => void,
  errorId: string | undefined,
): ReactElement {
  const ariaInvalid = errorId ? true : undefined;
  switch (field.type) {
    case 'select':
      return (
        <select
          id={inputId}
          value={asInputValue(value)}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={ariaInvalid}
          aria-describedby={errorId}
        >
          <option value="" disabled>
            Select…
          </option>
          {(field.options ?? []).map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))}
        </select>
      );
    case 'boolean':
      return (
        <label className="form-field__toggle">
          <input
            id={inputId}
            type="checkbox"
            checked={Boolean(value)}
            onChange={(e) => onChange(e.target.checked)}
            aria-invalid={ariaInvalid}
            aria-describedby={errorId}
          />
          <span>{Boolean(value) ? 'Enabled' : 'Disabled'}</span>
        </label>
      );
    case 'number':
      return (
        <input
          id={inputId}
          type="number"
          value={asInputValue(value)}
          placeholder={field.placeholder}
          onChange={(e) =>
            onChange(e.target.value === '' ? '' : Number(e.target.value))
          }
          aria-invalid={ariaInvalid}
          aria-describedby={errorId}
        />
      );
    case 'url':
      return (
        <input
          id={inputId}
          type="url"
          value={asInputValue(value)}
          placeholder={field.placeholder}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={ariaInvalid}
          aria-describedby={errorId}
        />
      );
    case 'text':
    default:
      return (
        <input
          id={inputId}
          type="text"
          value={asInputValue(value)}
          placeholder={field.placeholder}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={ariaInvalid}
          aria-describedby={errorId}
        />
      );
  }
}

export { validate as __validateForTests, buildPatch as __buildPatchForTests };
