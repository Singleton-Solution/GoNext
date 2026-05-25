'use client';

/**
 * Reset password — confirm a reset link and set a new password.
 *
 * The user lands here from an email link of the shape
 * `/reset-password?token=<hex>`. The token is captured from the
 * query string; the user supplies a new password (twice, with a
 * client-side equality check) and submits.
 *
 * Server-side validation (token validity, password strength) is
 * authoritative; the client mirrors the 12-char minimum so a typo
 * doesn't cost a server round-trip.
 */
import { Suspense, useEffect, useState, type ReactElement, type FormEvent } from 'react';
import { useSearchParams } from 'next/navigation';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ApiError, api } from '@/lib/api-client';

const MIN_PASSWORD_LENGTH = 12;

type SubmitState =
  | { status: 'idle' }
  | { status: 'submitting' }
  | { status: 'done' }
  | { status: 'error'; message: string };

function ResetPasswordForm(): ReactElement {
  const params = useSearchParams();
  const [token, setToken] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [submit, setSubmit] = useState<SubmitState>({ status: 'idle' });
  const [clientError, setClientError] = useState<string | null>(null);

  useEffect(() => {
    const t = params.get('token') ?? '';
    setToken(t);
  }, [params]);

  function validate(): string | null {
    if (token === '') {
      return 'This page is missing its reset token. Please request a new link.';
    }
    if (password.length < MIN_PASSWORD_LENGTH) {
      return `Password must be at least ${MIN_PASSWORD_LENGTH} characters.`;
    }
    if (password !== confirm) {
      return 'Passwords do not match.';
    }
    return null;
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    const local = validate();
    if (local !== null) {
      setClientError(local);
      return;
    }
    setClientError(null);
    setSubmit({ status: 'submitting' });
    try {
      await api.post('/api/v1/auth/password-reset/confirm', {
        token,
        new_password: password,
      });
      setSubmit({ status: 'done' });
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 410) {
          setSubmit({
            status: 'error',
            message:
              'This reset link is invalid or has expired. Please request a new one.',
          });
          return;
        }
        if (err.status === 422) {
          const detail =
            typeof err.payload === 'object' &&
            err.payload !== null &&
            'detail' in err.payload &&
            typeof (err.payload as { detail: unknown }).detail === 'string'
              ? (err.payload as { detail: string }).detail
              : 'Password does not meet the strength requirements.';
          setSubmit({ status: 'error', message: detail });
          return;
        }
        if (err.status === 429) {
          setSubmit({
            status: 'error',
            message:
              'Too many attempts. Please wait a few minutes before trying again.',
          });
          return;
        }
      }
      setSubmit({
        status: 'error',
        message: 'We could not reset your password. Please try again.',
      });
    }
  }

  if (submit.status === 'done') {
    return (
      <div className="flex flex-col gap-4">
        <div
          role="status"
          className="rounded-lg bg-paper-1 px-4 py-3 font-sans text-sm text-fg-default"
        >
          Your password has been updated and any active sessions were signed
          out. You can now sign in with your new password.
        </div>
        <Button asChild variant="emerald" size="lg">
          <a href="/login">Go to sign in</a>
        </Button>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} noValidate className="flex flex-col gap-4">
      <input type="hidden" name="token" value={token} readOnly />
      <div className="flex flex-col gap-[6px]">
        <Label htmlFor="password">New password</Label>
        <Input
          id="password"
          name="new_password"
          type="password"
          autoComplete="new-password"
          placeholder="At least 12 characters"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
          minLength={MIN_PASSWORD_LENGTH}
          disabled={submit.status === 'submitting'}
        />
      </div>
      <div className="flex flex-col gap-[6px]">
        <Label htmlFor="confirm">Confirm new password</Label>
        <Input
          id="confirm"
          name="confirm"
          type="password"
          autoComplete="new-password"
          placeholder="Re-enter it to confirm"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          required
          minLength={MIN_PASSWORD_LENGTH}
          disabled={submit.status === 'submitting'}
        />
      </div>
      {clientError !== null && (
        <p role="alert" className="font-sans text-sm text-red-600">
          {clientError}
        </p>
      )}
      {submit.status === 'error' && (
        <p role="alert" className="font-sans text-sm text-red-600">
          {submit.message}
        </p>
      )}
      <Button
        type="submit"
        variant="emerald"
        size="lg"
        className="mt-2"
        disabled={submit.status === 'submitting' || token === ''}
      >
        {submit.status === 'submitting' ? 'Updating…' : 'Set new password'}
      </Button>
    </form>
  );
}

export default function ResetPasswordPage(): ReactElement {
  return (
    <section className="login-card relative mx-auto w-full max-w-[420px]">
      <div
        aria-hidden="true"
        className="pointer-events-none fixed -top-[20%] -right-[15%] -z-10 h-[600px] w-[600px] rounded-pill"
        style={{
          background:
            'radial-gradient(circle, rgba(16, 185, 129, 0.10) 0%, transparent 60%)',
        }}
      />
      <div
        aria-hidden="true"
        className="pointer-events-none fixed -bottom-[20%] -left-[15%] -z-10 h-[600px] w-[600px] rounded-pill"
        style={{
          background:
            'radial-gradient(circle, rgba(167, 139, 250, 0.08) 0%, transparent 60%)',
        }}
      />

      <Card className="overflow-hidden bg-paper-2 shadow-md rounded-xl border-border">
        <CardContent className="px-7 py-8">
          <div className="mb-6 flex flex-col gap-3 text-left">
            <span className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep">
              GoNext admin
            </span>
            <Headline as="h1" size="sub">
              Choose a new <em>password</em>.
            </Headline>
            <p className="font-sans text-sm text-fg-muted">
              Pick something memorable but hard to guess — at least{' '}
              {MIN_PASSWORD_LENGTH} characters.
            </p>
          </div>

          {/* useSearchParams must be wrapped in a Suspense boundary so the
              page can be statically rendered. */}
          <Suspense fallback={<p className="font-sans text-sm">Loading…</p>}>
            <ResetPasswordForm />
          </Suspense>

          <div className="mt-6 border-t border-border pt-4 text-center font-sans text-xs text-fg-subtle">
            <a className="text-emerald-deep" href="/login">Back to sign in</a>
          </div>
        </CardContent>
      </Card>
    </section>
  );
}
