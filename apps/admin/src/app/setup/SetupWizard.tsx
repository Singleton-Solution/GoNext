'use client';

/**
 * SetupWizard — the in-browser first-run install flow.
 *
 * The wizard is a five-step linear form:
 *
 *   1. Welcome — system-check summary + "Begin" CTA.
 *   2. Admin — email + password (with the strength meter from
 *      ./components/StrengthMeter).
 *   3. Site — name + URL.
 *   4. Review — read-only recap with a "Confirm install" CTA.
 *   5. Done — success state with an auto-redirect to /.
 *
 * Each step has its own validation; "Next" is gated on the step's
 * fields passing locally before we let the operator advance. The
 * server is the authoritative gate (12-char password floor, URL parse,
 * etc.); the per-step validation is a UX layer on top.
 *
 * On submit the wizard POSTs the full payload to
 * /api/v1/setup/install. A 200 carries the session cookie (HttpOnly)
 * and the wizard navigates to /. A 4xx renders the server's `message`
 * on the step the `code` maps to so the operator can correct the
 * specific field without losing the rest of their input.
 *
 * The component is a Client Component because every step has either an
 * input event handler or a router.push call — both of which require
 * client runtime.
 */
import { useState, type FormEvent, type ReactElement } from 'react';
import { useRouter } from 'next/navigation';
import { apiBaseUrl } from '../api-client';
import type { InstallError, InstallResponse, SetupStatus } from './types';
import { scorePassword, StrengthMeter } from './components/StrengthMeter';
import { SystemCheck } from './components/SystemCheck';

const STEPS = ['welcome', 'admin', 'site', 'review', 'done'] as const;
type Step = (typeof STEPS)[number];

interface FormState {
  adminEmail: string;
  adminPassword: string;
  siteName: string;
  siteURL: string;
}

const INITIAL_FORM: FormState = {
  adminEmail: '',
  adminPassword: '',
  siteName: '',
  siteURL: '',
};

/**
 * Maps server error codes to the wizard step the operator should
 * return to. Codes the wizard hasn't seen before default to 'admin'
 * because that's the most common point of failure (weak password,
 * malformed email).
 */
function stepForErrorCode(code: string): Step {
  switch (code) {
    case 'invalid_email':
    case 'weak_password':
      return 'admin';
    case 'invalid_site_name':
    case 'invalid_site_url':
      return 'site';
    case 'already_installed':
      // The lock closed between mount and submit — there's nothing the
      // operator can do here; we'll route them to login after rendering
      // the error.
      return 'review';
    default:
      return 'admin';
  }
}

/**
 * Minimal client-side email format check. Mirrors the server's
 * looksLikeEmail (one '@', host with a dot, no whitespace). We do NOT
 * regex against RFC 5322 — see the comment on the server function for
 * why.
 */
export function isProbablyEmail(s: string): boolean {
  if (!s) return false;
  const at = s.indexOf('@');
  if (at <= 0 || at === s.length - 1) return false;
  const host = s.slice(at + 1);
  if (!host.includes('.')) return false;
  if (/\s/.test(s)) return false;
  return true;
}

/**
 * Minimal URL check: must parse and use http or https.
 */
export function isProbablyURL(s: string): boolean {
  if (!s) return false;
  try {
    const u = new URL(s);
    return u.protocol === 'http:' || u.protocol === 'https:';
  } catch {
    return false;
  }
}

export interface SetupWizardProps {
  /**
   * Status payload pre-fetched on the server, threaded through to the
   * welcome step so the operator sees a system check without an extra
   * client round-trip.
   */
  initialStatus: SetupStatus;
}

export default function SetupWizard({ initialStatus }: SetupWizardProps): ReactElement {
  const router = useRouter();
  const [step, setStep] = useState<Step>('welcome');
  const [form, setForm] = useState<FormState>(INITIAL_FORM);
  const [submitting, setSubmitting] = useState<boolean>(false);
  const [serverError, setServerError] = useState<InstallError | null>(null);

  const passwordScore = scorePassword(form.adminPassword);
  const adminValid =
    isProbablyEmail(form.adminEmail) && passwordScore >= 2;
  const siteValid =
    form.siteName.trim().length > 0 && isProbablyURL(form.siteURL.trim());

  function goNext(next: Step): void {
    setServerError(null);
    setStep(next);
  }

  function goBack(prev: Step): void {
    setServerError(null);
    setStep(prev);
  }

  async function handleSubmit(event: FormEvent): Promise<void> {
    event.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    setServerError(null);
    try {
      const response = await fetch(`${apiBaseUrl}/api/v1/setup/install`, {
        method: 'POST',
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          Accept: 'application/json',
        },
        body: JSON.stringify({
          admin_email: form.adminEmail.trim().toLowerCase(),
          admin_password: form.adminPassword,
          site_name: form.siteName.trim(),
          site_url: form.siteURL.trim(),
        }),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as Partial<InstallError>;
        const code = body.code ?? 'error';
        const message =
          body.message ?? `Install failed (HTTP ${response.status}).`;
        setServerError({ code, message });
        setStep(stepForErrorCode(code));
        return;
      }
      // 200 → cookie set by Set-Cookie. Move to the success step and
      // schedule the redirect so the operator sees the confirmation
      // for a beat before we navigate away.
      (await response.json()) as InstallResponse;
      setStep('done');
      // Defer to avoid surprising the operator with an instant flash.
      window.setTimeout(() => {
        router.push('/');
      }, 1500);
    } catch (err) {
      setServerError({
        code: 'network_error',
        message:
          err instanceof Error
            ? `Could not reach the API: ${err.message}`
            : 'Could not reach the API.',
      });
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <section className="setup">
      <header className="setup__header">
        <h1>Set up GoNext</h1>
        <p className="muted">
          One-time first-run wizard. Once installed, this page is locked.
        </p>
        <StepIndicator current={step} />
      </header>
      <div className="setup__step">
        {step === 'welcome' ? (
          <WelcomeStep status={initialStatus} onNext={(): void => goNext('admin')} />
        ) : null}
        {step === 'admin' ? (
          <AdminStep
            form={form}
            setForm={setForm}
            passwordScore={passwordScore}
            error={serverError}
            canAdvance={adminValid}
            onBack={(): void => goBack('welcome')}
            onNext={(): void => goNext('site')}
          />
        ) : null}
        {step === 'site' ? (
          <SiteStep
            form={form}
            setForm={setForm}
            error={serverError}
            canAdvance={siteValid}
            onBack={(): void => goBack('admin')}
            onNext={(): void => goNext('review')}
          />
        ) : null}
        {step === 'review' ? (
          <ReviewStep
            form={form}
            error={serverError}
            submitting={submitting}
            onBack={(): void => goBack('site')}
            onSubmit={handleSubmit}
          />
        ) : null}
        {step === 'done' ? <DoneStep /> : null}
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Sub-components — colocated rather than split into files. Each one is
// pure-presentational: the parent owns state, the children only render
// and emit callbacks.
// ---------------------------------------------------------------------------

interface StepIndicatorProps {
  current: Step;
}

function StepIndicator({ current }: StepIndicatorProps): ReactElement {
  const labels: Record<Step, string> = {
    welcome: 'Welcome',
    admin: 'Admin',
    site: 'Site',
    review: 'Review',
    done: 'Done',
  };
  const idx = STEPS.indexOf(current);
  return (
    <ol className="setup-steps" aria-label="Setup progress">
      {STEPS.map((s, i) => (
        <li
          key={s}
          className={
            i === idx
              ? 'setup-steps__item setup-steps__item--current'
              : i < idx
                ? 'setup-steps__item setup-steps__item--done'
                : 'setup-steps__item'
          }
          aria-current={i === idx ? 'step' : undefined}
        >
          <span className="setup-steps__num">{i + 1}</span>
          <span className="setup-steps__label">{labels[s]}</span>
        </li>
      ))}
    </ol>
  );
}

interface WelcomeStepProps {
  status: SetupStatus;
  onNext: () => void;
}

function WelcomeStep({ status, onNext }: WelcomeStepProps): ReactElement {
  return (
    <div className="setup-step">
      <h2>Welcome to GoNext</h2>
      <p>
        This wizard creates the bootstrap administrator and stamps the
        installation marker. It runs once — after a successful install
        this page returns to the login screen.
      </p>
      <SystemCheck status={status} />
      <div className="setup-step__actions">
        <button type="button" className="btn-primary" onClick={onNext}>
          Begin
        </button>
      </div>
    </div>
  );
}

interface AdminStepProps {
  form: FormState;
  setForm: (next: FormState) => void;
  passwordScore: ReturnType<typeof scorePassword>;
  error: InstallError | null;
  canAdvance: boolean;
  onBack: () => void;
  onNext: () => void;
}

function AdminStep({
  form,
  setForm,
  error,
  canAdvance,
  onBack,
  onNext,
}: AdminStepProps): ReactElement {
  const emailError =
    form.adminEmail.length > 0 && !isProbablyEmail(form.adminEmail)
      ? 'Enter a valid email (e.g. you@example.com).'
      : null;
  return (
    <form
      className="setup-step"
      onSubmit={(e): void => {
        e.preventDefault();
        if (canAdvance) onNext();
      }}
      noValidate
    >
      <h2>Administrator</h2>
      <p className="muted">
        This account becomes the super_admin. Pick a long passphrase — short
        passwords are refused server-side regardless of what the meter
        reports.
      </p>
      {error && (error.code === 'invalid_email' || error.code === 'weak_password') ? (
        <p className="setup-step__error" role="alert">
          {error.message}
        </p>
      ) : null}
      <div className="form-field">
        <label htmlFor="setup-email">Email</label>
        <input
          id="setup-email"
          name="admin_email"
          type="email"
          autoComplete="username"
          required
          value={form.adminEmail}
          onChange={(e): void => setForm({ ...form, adminEmail: e.target.value })}
        />
        {emailError ? (
          <span className="setup-step__hint" role="status">
            {emailError}
          </span>
        ) : null}
      </div>
      <div className="form-field">
        <label htmlFor="setup-password">Password</label>
        <input
          id="setup-password"
          name="admin_password"
          type="password"
          autoComplete="new-password"
          required
          minLength={12}
          aria-describedby="setup-password-strength"
          value={form.adminPassword}
          onChange={(e): void => setForm({ ...form, adminPassword: e.target.value })}
        />
        <StrengthMeter password={form.adminPassword} describedFor="setup-password" />
      </div>
      <div className="setup-step__actions">
        <button type="button" className="btn-secondary" onClick={onBack}>
          Back
        </button>
        <button type="submit" className="btn-primary" disabled={!canAdvance}>
          Continue
        </button>
      </div>
    </form>
  );
}

interface SiteStepProps {
  form: FormState;
  setForm: (next: FormState) => void;
  error: InstallError | null;
  canAdvance: boolean;
  onBack: () => void;
  onNext: () => void;
}

function SiteStep({
  form,
  setForm,
  error,
  canAdvance,
  onBack,
  onNext,
}: SiteStepProps): ReactElement {
  const urlError =
    form.siteURL.length > 0 && !isProbablyURL(form.siteURL)
      ? 'Enter an absolute URL with http or https.'
      : null;
  return (
    <form
      className="setup-step"
      onSubmit={(e): void => {
        e.preventDefault();
        if (canAdvance) onNext();
      }}
      noValidate
    >
      <h2>Site</h2>
      <p className="muted">
        You can change these later from Settings → General; they ship as
        the first values your visitors see.
      </p>
      {error && (error.code === 'invalid_site_name' || error.code === 'invalid_site_url') ? (
        <p className="setup-step__error" role="alert">
          {error.message}
        </p>
      ) : null}
      <div className="form-field">
        <label htmlFor="setup-site-name">Site name</label>
        <input
          id="setup-site-name"
          name="site_name"
          type="text"
          required
          maxLength={200}
          value={form.siteName}
          onChange={(e): void => setForm({ ...form, siteName: e.target.value })}
        />
      </div>
      <div className="form-field">
        <label htmlFor="setup-site-url">Site URL</label>
        <input
          id="setup-site-url"
          name="site_url"
          type="url"
          required
          placeholder="https://example.com"
          value={form.siteURL}
          onChange={(e): void => setForm({ ...form, siteURL: e.target.value })}
        />
        {urlError ? (
          <span className="setup-step__hint" role="status">
            {urlError}
          </span>
        ) : null}
      </div>
      <div className="setup-step__actions">
        <button type="button" className="btn-secondary" onClick={onBack}>
          Back
        </button>
        <button type="submit" className="btn-primary" disabled={!canAdvance}>
          Continue
        </button>
      </div>
    </form>
  );
}

interface ReviewStepProps {
  form: FormState;
  error: InstallError | null;
  submitting: boolean;
  onBack: () => void;
  onSubmit: (event: FormEvent) => Promise<void>;
}

function ReviewStep({
  form,
  error,
  submitting,
  onBack,
  onSubmit,
}: ReviewStepProps): ReactElement {
  return (
    <form className="setup-step" onSubmit={onSubmit} noValidate>
      <h2>Review</h2>
      {error ? (
        <p className="setup-step__error" role="alert">
          {error.message ?? 'Install failed.'}
        </p>
      ) : null}
      <dl className="setup-review">
        <dt>Admin email</dt>
        <dd>{form.adminEmail}</dd>
        <dt>Site name</dt>
        <dd>{form.siteName}</dd>
        <dt>Site URL</dt>
        <dd>{form.siteURL}</dd>
        <dt>Password</dt>
        <dd>
          <span className="muted">stored as argon2id</span>
        </dd>
      </dl>
      <div className="setup-step__actions">
        <button
          type="button"
          className="btn-secondary"
          onClick={onBack}
          disabled={submitting}
        >
          Back
        </button>
        <button type="submit" className="btn-primary" disabled={submitting}>
          {submitting ? 'Installing…' : 'Confirm install'}
        </button>
      </div>
    </form>
  );
}

function DoneStep(): ReactElement {
  return (
    <div className="setup-step">
      <h2>You are in</h2>
      <p>
        Install completed. Redirecting to the admin dashboard…
      </p>
    </div>
  );
}
