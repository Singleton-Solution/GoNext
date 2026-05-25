'use client';

/**
 * Forgot password — request a reset link.
 *
 * Single field (email) + submit. The API response is always 200 even
 * when the email is unknown (enumeration-safe), so the UI shows the
 * same success state regardless of input validity. This is intentional:
 * "we sent a link to that address if it's registered" leaks nothing,
 * "no account found" leaks an account existence signal.
 *
 * Visual treatment mirrors /login (centered paper-2 card, brand glows,
 * eyebrow + headline + helper copy) so users moving between the two
 * surfaces don't experience a context switch.
 */
import { useState, type ReactElement, type FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ApiError, api } from '@/lib/api-client';

type SubmitState =
  | { status: 'idle' }
  | { status: 'submitting' }
  | { status: 'sent' }
  | { status: 'error'; message: string };

export default function ForgotPasswordPage(): ReactElement {
  const [email, setEmail] = useState('');
  const [submit, setSubmit] = useState<SubmitState>({ status: 'idle' });

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    setSubmit({ status: 'submitting' });
    try {
      await api.post('/api/v1/auth/password-reset/request', { email });
      setSubmit({ status: 'sent' });
    } catch (err) {
      // 429 is the only non-200 the API issues here — render it
      // as a friendly retry message rather than the raw status.
      if (err instanceof ApiError && err.status === 429) {
        setSubmit({
          status: 'error',
          message:
            'Too many attempts from this address. Please wait a few minutes and try again.',
        });
        return;
      }
      setSubmit({
        status: 'error',
        message:
          'We could not send the link. Please check your connection and try again.',
      });
    }
  }

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
              Reset your <em>password</em>.
            </Headline>
            <p className="font-sans text-sm text-fg-muted">
              Enter the email tied to your account and we&apos;ll send you a
              one-time reset link.
            </p>
          </div>

          {submit.status === 'sent' ? (
            <div
              role="status"
              className="rounded-lg bg-paper-1 px-4 py-3 font-sans text-sm text-fg-default"
            >
              If an account exists for that address, a reset link is on the
              way. The link expires in 1 hour and can only be used once.
            </div>
          ) : (
            <form onSubmit={handleSubmit} noValidate className="flex flex-col gap-4">
              <div className="flex flex-col gap-[6px]">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  name="email"
                  type="email"
                  autoComplete="username"
                  placeholder="you@example.com"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                  disabled={submit.status === 'submitting'}
                />
              </div>
              {submit.status === 'error' && (
                <p
                  role="alert"
                  className="font-sans text-sm text-red-600"
                >
                  {submit.message}
                </p>
              )}
              <Button
                type="submit"
                variant="emerald"
                size="lg"
                className="mt-2"
                disabled={submit.status === 'submitting' || email === ''}
              >
                {submit.status === 'submitting' ? 'Sending…' : 'Send reset link'}
              </Button>
            </form>
          )}

          <div className="mt-6 border-t border-border pt-4 text-center font-sans text-xs text-fg-subtle">
            Remembered it? <a className="text-emerald-deep" href="/login">Back to sign in</a>.
          </div>
        </CardContent>
      </Card>
    </section>
  );
}
