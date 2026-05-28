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
 * Visual treatment follows docs/design/ui_kits/onboarding/index.html —
 * the canonical onboarding hero. Cream paper background with soft
 * off-canvas emerald + lavender radial glows, a centered paper-2 card
 * holding each step, an Archivo headline with the italic-accent rule
 * for every step heading, Geist body, Archivo button labels, Lucide
 * icons throughout. The top strip mirrors the onboarding step
 * indicator: numbered pills connected by hairlines, the current step
 * inked, completed steps emerald with a check glyph.
 *
 * The component is a Client Component because every step has either an
 * input event handler or a router.push call — both of which require
 * client runtime.
 */
import { useState, type FormEvent, type ReactElement } from 'react';
import { useRouter } from 'next/navigation';
import {
  ArrowLeft,
  ArrowRight,
  Check,
  CheckCircle2,
  Loader2,
  ShieldCheck,
  Sparkles,
} from 'lucide-react';

import { apiBaseUrl } from '@/lib/api-client';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
      const response = await fetch(`${apiBaseUrl()}/api/v1/setup/install`, {
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
    <section
      className={cn(
        'setup',
        'relative mx-auto w-full max-w-[760px]',
      )}
    >
      {/* Soft brand glows tucked behind the card so the cream surface
          feels alive without overwhelming the form. Matches the
          off-canvas emerald + lavender radial gradients on the
          canonical onboarding hero. */}
      <div
        aria-hidden="true"
        className="pointer-events-none fixed -top-[20%] -right-[15%] -z-10 h-[700px] w-[700px]"
        style={{
          background:
            'radial-gradient(circle, rgba(16, 185, 129, 0.10) 0%, transparent 60%)',
        }}
      />
      <div
        aria-hidden="true"
        className="pointer-events-none fixed -bottom-[25%] -left-[15%] -z-10 h-[600px] w-[600px]"
        style={{
          background:
            'radial-gradient(circle, rgba(167, 139, 250, 0.08) 0%, transparent 60%)',
        }}
      />

      <header className={cn('setup__header', 'mb-6 flex flex-col gap-5')}>
        <div className="flex items-center justify-between gap-4">
          <Wordmark />
          <span className="font-sans text-xs uppercase tracking-[0.12em] text-fg-subtle">
            First-run setup
          </span>
        </div>
        <StepIndicator current={step} />
        {/* Preserve the legacy h1 in the DOM (visually hidden) so any
            tooling that scans for "Set up GoNext" still sees it; the
            brand surface uses per-step Headlines instead. */}
        <h1 className="sr-only">Set up GoNext</h1>
      </header>

      <div
        className={cn(
          'setup__step',
          'rounded-xl border border-border bg-paper-2 shadow-md',
          'px-8 py-8 sm:px-10 sm:py-10',
        )}
      >
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

/**
 * Wordmark — composite of Archivo `Go` and italic-serif `Next` per the
 * brand handoff. Inline rather than imported so the wizard has no
 * cross-route dependency on the authenticated Sidebar's copy.
 */
function Wordmark(): ReactElement {
  return (
    <span
      className="inline-flex items-baseline gap-[1px] leading-none"
      aria-label="GoNext"
    >
      <span className="font-display text-[19px] font-extrabold tracking-tight text-ink">
        Go
      </span>
      <span className="font-serif text-[21px] italic text-ink">
        Next
      </span>
    </span>
  );
}

interface StepIndicatorProps {
  current: Step;
}

/**
 * Mirrors `.topstrip .steps` from the canonical onboarding HTML: a
 * row of numbered pills (or check glyphs once complete) joined by
 * hairlines. Done = emerald, current = ink, pending = paper-3.
 */
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
    <ol
      className={cn(
        'setup-steps',
        'flex w-full items-center gap-0 list-none p-0 m-0',
      )}
      aria-label="Setup progress"
    >
      {STEPS.map((s, i) => {
        const state =
          i === idx ? 'current' : i < idx ? 'done' : 'pending';
        const isFirst = i === 0;
        return (
          <li
            key={s}
            className={cn(
              'setup-steps__item',
              state === 'current' && 'setup-steps__item--current',
              state === 'done' && 'setup-steps__item--done',
              'relative flex flex-1 items-center gap-2 text-sm',
            )}
            aria-current={state === 'current' ? 'step' : undefined}
          >
            {/* Connector to the previous step. */}
            {!isFirst ? (
              <span
                aria-hidden="true"
                className="h-px flex-1 bg-border"
              />
            ) : null}
            <span className="flex items-center gap-2 px-2">
              <span
                className={cn(
                  'setup-steps__num',
                  'flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-pill border font-mono text-[10px] font-medium',
                  state === 'pending' &&
                    'bg-paper-3 border-border text-fg-muted',
                  state === 'current' &&
                    'bg-ink border-ink text-paper',
                  state === 'done' &&
                    'bg-emerald border-emerald text-emerald-ink',
                )}
              >
                {state === 'done' ? (
                  <Check width={11} height={11} strokeWidth={3} />
                ) : (
                  String(i + 1).padStart(2, '0')
                )}
              </span>
              <span
                className={cn(
                  'setup-steps__label',
                  'font-sans font-medium',
                  state === 'current' ? 'text-ink' : 'text-fg-muted',
                )}
              >
                {labels[s]}
              </span>
            </span>
          </li>
        );
      })}
    </ol>
  );
}

interface WelcomeStepProps {
  status: SetupStatus;
  onNext: () => void;
}

function WelcomeStep({ status, onNext }: WelcomeStepProps): ReactElement {
  return (
    <div className="setup-step flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          Welcome — let&apos;s begin
        </span>
        {/* "Welcome to GoNext" sits in the heading as a single text
            node so screen-reader and getByText callers see one
            uninterrupted phrase. The italic accent lands on the
            verb-mood word "begin" — the brand's signature emphasis
            without splitting the product name across two type families. */}
        <Headline as="h2" size="sub">
          Welcome to GoNext. <em>Let&apos;s begin</em>.
        </Headline>
        <p className="font-sans text-md text-fg-muted">
          This wizard creates the bootstrap administrator and stamps the
          installation marker. It runs once — after a successful install
          this page returns to the login screen.
        </p>
      </div>
      <SystemCheck status={status} />
      <div className="setup-step__actions flex items-center justify-end gap-3 border-t border-border pt-5">
        <Button
          type="button"
          variant="primary"
          size="lg"
          className="btn-primary"
          onClick={onNext}
        >
          Begin
          <ArrowRight width={14} height={14} aria-hidden="true" />
        </Button>
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
      className="setup-step flex flex-col gap-6"
      onSubmit={(e): void => {
        e.preventDefault();
        if (canAdvance) onNext();
      }}
      noValidate
    >
      <div className="flex flex-col gap-3">
        <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          Step 02 — Identity
        </span>
        {/* Heading reads "First Administrator" so the existing test
            (getByRole('heading', { name: /Administrator/i })) still
            resolves; the italic accent lands on "First" — the brand's
            signature emphasis. */}
        <Headline as="h2" size="sub">
          <em>First</em> Administrator
        </Headline>
        <p className="font-sans text-md text-fg-muted">
          This account becomes the super_admin. Pick a long passphrase —
          short passwords are refused server-side regardless of what the
          meter reports.
        </p>
      </div>
      {error && (error.code === 'invalid_email' || error.code === 'weak_password') ? (
        <p
          className={cn(
            'setup-step__error',
            'rounded-md border border-danger/30 bg-danger-soft px-4 py-3 font-sans text-sm text-danger',
          )}
          role="alert"
        >
          {error.message}
        </p>
      ) : null}
      <div className="form-field flex flex-col gap-[6px]">
        <Label htmlFor="setup-email">Email</Label>
        <Input
          id="setup-email"
          name="admin_email"
          type="email"
          autoComplete="username"
          placeholder="you@example.com"
          required
          value={form.adminEmail}
          onChange={(e): void => setForm({ ...form, adminEmail: e.target.value })}
        />
        {emailError ? (
          <span
            className={cn(
              'setup-step__hint',
              'mt-1 font-sans text-xs text-danger',
            )}
            role="status"
          >
            {emailError}
          </span>
        ) : null}
      </div>
      <div className="form-field flex flex-col gap-[6px]">
        <Label htmlFor="setup-password">Password</Label>
        <Input
          id="setup-password"
          name="admin_password"
          type="password"
          autoComplete="new-password"
          placeholder="At least 12 characters"
          required
          minLength={12}
          aria-describedby="setup-password-strength"
          value={form.adminPassword}
          onChange={(e): void => setForm({ ...form, adminPassword: e.target.value })}
        />
        <StrengthMeter password={form.adminPassword} describedFor="setup-password" />
      </div>
      <div className="setup-step__actions flex items-center justify-between gap-3 border-t border-border pt-5">
        <Button
          type="button"
          variant="default"
          className="btn-secondary"
          onClick={onBack}
        >
          <ArrowLeft width={14} height={14} aria-hidden="true" />
          Back
        </Button>
        <Button
          type="submit"
          variant="primary"
          size="lg"
          className="btn-primary"
          disabled={!canAdvance}
        >
          Continue
          <ArrowRight width={14} height={14} aria-hidden="true" />
        </Button>
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
      className="setup-step flex flex-col gap-6"
      onSubmit={(e): void => {
        e.preventDefault();
        if (canAdvance) onNext();
      }}
      noValidate
    >
      <div className="flex flex-col gap-3">
        <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          Step 03 — Identity
        </span>
        {/* Heading text is the bare word "Site" so the existing test
            (getByRole('heading', { name: /^Site$/i })) keeps passing.
            The brand's signature italic-serif accent is applied to the
            word itself — emphasis without changing the accessible name. */}
        <Headline as="h2" size="sub">
          <em>Site</em>
        </Headline>
        <p className="font-sans text-md text-fg-muted">
          You can change these later from Settings → General; they ship
          as the first values your visitors see.
        </p>
      </div>
      {error && (error.code === 'invalid_site_name' || error.code === 'invalid_site_url') ? (
        <p
          className={cn(
            'setup-step__error',
            'rounded-md border border-danger/30 bg-danger-soft px-4 py-3 font-sans text-sm text-danger',
          )}
          role="alert"
        >
          {error.message}
        </p>
      ) : null}
      <div className="form-field flex flex-col gap-[6px]">
        <Label htmlFor="setup-site-name">Site name</Label>
        <Input
          id="setup-site-name"
          name="site_name"
          type="text"
          placeholder="Brick &amp; Mortar"
          required
          maxLength={200}
          value={form.siteName}
          onChange={(e): void => setForm({ ...form, siteName: e.target.value })}
        />
      </div>
      <div className="form-field flex flex-col gap-[6px]">
        <Label htmlFor="setup-site-url">Site URL</Label>
        <Input
          id="setup-site-url"
          name="site_url"
          type="url"
          required
          placeholder="https://example.com"
          value={form.siteURL}
          onChange={(e): void => setForm({ ...form, siteURL: e.target.value })}
        />
        {urlError ? (
          <span
            className={cn(
              'setup-step__hint',
              'mt-1 font-sans text-xs text-danger',
            )}
            role="status"
          >
            {urlError}
          </span>
        ) : null}
      </div>
      <div className="setup-step__actions flex items-center justify-between gap-3 border-t border-border pt-5">
        <Button
          type="button"
          variant="default"
          className="btn-secondary"
          onClick={onBack}
        >
          <ArrowLeft width={14} height={14} aria-hidden="true" />
          Back
        </Button>
        <Button
          type="submit"
          variant="primary"
          size="lg"
          className="btn-primary"
          disabled={!canAdvance}
        >
          Continue
          <ArrowRight width={14} height={14} aria-hidden="true" />
        </Button>
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
    <form
      className="setup-step flex flex-col gap-6"
      onSubmit={onSubmit}
      noValidate
    >
      <div className="flex flex-col gap-3">
        <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
          Step 04 — Confirm
        </span>
        <Headline as="h2" size="sub">
          One last <em>look</em>. Review.
        </Headline>
        <p className="font-sans text-md text-fg-muted">
          Everything below ships to the API on confirm. The install lock
          seals after a 200 — you can&apos;t re-run this wizard.
        </p>
      </div>
      {error ? (
        <p
          className={cn(
            'setup-step__error',
            'rounded-md border border-danger/30 bg-danger-soft px-4 py-3 font-sans text-sm text-danger',
          )}
          role="alert"
        >
          {error.message ?? 'Install failed.'}
        </p>
      ) : null}
      <dl
        className={cn(
          'setup-review',
          'grid grid-cols-[160px_1fr] gap-x-5 gap-y-3 rounded-lg border border-border bg-paper px-5 py-4',
        )}
      >
        <dt className="font-sans text-xs uppercase tracking-[0.08em] text-fg-subtle">
          Admin email
        </dt>
        <dd className="font-mono text-sm text-ink">{form.adminEmail}</dd>
        <dt className="font-sans text-xs uppercase tracking-[0.08em] text-fg-subtle">
          Site name
        </dt>
        <dd className="font-sans text-sm font-medium text-ink">{form.siteName}</dd>
        <dt className="font-sans text-xs uppercase tracking-[0.08em] text-fg-subtle">
          Site URL
        </dt>
        <dd className="font-mono text-sm text-ink">{form.siteURL}</dd>
        <dt className="font-sans text-xs uppercase tracking-[0.08em] text-fg-subtle">
          Password
        </dt>
        <dd className="flex items-center gap-2 font-sans text-sm text-fg-muted">
          <ShieldCheck
            width={14}
            height={14}
            className="text-emerald-deep"
            aria-hidden="true"
          />
          <span className="muted">stored as argon2id</span>
        </dd>
      </dl>
      <div className="setup-step__actions flex items-center justify-between gap-3 border-t border-border pt-5">
        <Button
          type="button"
          variant="default"
          className="btn-secondary"
          onClick={onBack}
          disabled={submitting}
        >
          <ArrowLeft width={14} height={14} aria-hidden="true" />
          Back
        </Button>
        <Button
          type="submit"
          variant="emerald"
          size="lg"
          className="btn-primary"
          disabled={submitting}
        >
          {submitting ? (
            <>
              <Loader2
                width={14}
                height={14}
                className="animate-spin"
                aria-hidden="true"
              />
              Installing…
            </>
          ) : (
            <>
              Confirm install
              <ArrowRight width={14} height={14} aria-hidden="true" />
            </>
          )}
        </Button>
      </div>
    </form>
  );
}

function DoneStep(): ReactElement {
  return (
    <div className="setup-step flex flex-col items-center gap-5 py-6 text-center">
      <span
        className="flex h-14 w-14 items-center justify-center rounded-pill bg-emerald-soft text-emerald-deep"
        aria-hidden="true"
      >
        <CheckCircle2 width={28} height={28} strokeWidth={2.25} />
      </span>
      <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
        Installed — welcome aboard
      </span>
      <Headline as="h2" size="sub">
        You are <em>in</em>.
      </Headline>
      <p className="font-sans text-md text-fg-muted max-w-[420px]">
        Install completed. Redirecting to the admin dashboard…
      </p>
      <span
        className="mt-2 inline-flex items-center gap-2 font-sans text-xs text-fg-subtle"
        aria-live="polite"
      >
        <Sparkles
          width={12}
          height={12}
          className="text-emerald-deep"
          aria-hidden="true"
        />
        Spinning up your workspace
      </span>
    </div>
  );
}
