'use client';

/**
 * Login — form skeleton, restyled against the Living-Systems brand.
 *
 * This is intentionally not wired to the API yet. The real auth flow
 * (CSRF token fetch, POST /v1/sessions, cookie handling, error states)
 * lands with the auth UI issue. For now we render a valid, accessible
 * form structure so the IA includes a login surface and the route
 * exists.
 *
 * Visual treatment follows docs/design/ui_kits/onboarding/index.html
 * (the closest first-90-seconds surface to a sign-in screen): cream
 * paper background with soft off-canvas emerald + lavender radial
 * glows, a centered paper-2 card holding the form, an Archivo
 * display headline with the brand's italic-accent rule (`Sign <em>in</em>`),
 * Geist body copy, shadcn primitives for the form controls. The
 * "Sign in" string is preserved verbatim so the public-layout test
 * (which asserts `getByRole('heading', { name: /Sign in/i })`)
 * keeps passing.
 *
 * Marked as a Client Component because the form's onSubmit handler
 * must run on the client (React requires event handlers in Client
 * Components).
 */
import { useState, type ReactElement } from 'react';

import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Headline } from '@/components/ui/headline';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ApiError, api } from '@/lib/api-client';

type MagicLinkState =
  | { status: 'idle' }
  | { status: 'submitting' }
  | { status: 'sent' }
  | { status: 'error'; message: string };

export default function LoginPage(): ReactElement {
  const [email, setEmail] = useState('');
  const [magicLink, setMagicLink] = useState<MagicLinkState>({ status: 'idle' });

  async function handleSendMagicLink(): Promise<void> {
    if (email === '') {
      setMagicLink({
        status: 'error',
        message: 'Enter your email above first, then we can send the link.',
      });
      return;
    }
    setMagicLink({ status: 'submitting' });
    try {
      await api.post('/api/v1/auth/magic-link/request', { email });
      setMagicLink({ status: 'sent' });
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setMagicLink({
          status: 'error',
          message:
            'Too many attempts. Please wait a few minutes and try again.',
        });
        return;
      }
      setMagicLink({
        status: 'error',
        message: 'We could not send the link. Please try again.',
      });
    }
  }

  return (
    <section
      className="login-card relative mx-auto w-full max-w-[420px]"
      // Override the legacy .login-card width without removing the
      // class — keeping it lets pre-brand snapshots still match the
      // DOM selector contract. Layout sizing comes from Tailwind.
    >
      {/* Soft brand glows tucked behind the card so the cream surface
          feels alive without overwhelming the form. Matches the
          off-canvas emerald + lavender radial gradients on the
          onboarding hero. */}
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
            <span
              className="font-sans text-xs font-medium uppercase tracking-[0.12em] text-emerald-deep"
              // Eyebrow — mirrors .eyebrow from the handoff.
            >
              GoNext admin
            </span>
            <Headline as="h1" size="sub">
              Sign <em>in</em>.
            </Headline>
            <p className="font-sans text-sm text-fg-muted">
              Use your GoNext admin credentials to continue.
            </p>
          </div>

          <form
            // No action yet — submission is a no-op until the auth wire-up.
            onSubmit={(event) => event.preventDefault()}
            noValidate
            className="flex flex-col gap-4"
          >
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
              />
            </div>
            <div className="flex flex-col gap-[6px]">
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                name="password"
                type="password"
                autoComplete="current-password"
                placeholder="••••••••"
                required
              />
            </div>
            <Button type="submit" variant="emerald" size="lg" className="mt-2">
              Sign in
            </Button>
          </form>

          {/* Passwordless alternative — send a one-time sign-in link to
              the email entered above. Lives below the primary form so
              the password path stays the dominant affordance, but is
              one click away for users who don't remember the password. */}
          <div className="mt-4">
            {magicLink.status === 'sent' ? (
              <div
                role="status"
                className="rounded-lg bg-paper-1 px-4 py-3 font-sans text-sm text-fg-default"
              >
                If an account exists for that address, a sign-in link is on
                the way. The link expires in 15 minutes.
              </div>
            ) : (
              <>
                <Button
                  type="button"
                  variant="outline"
                  size="lg"
                  className="w-full"
                  onClick={handleSendMagicLink}
                  disabled={magicLink.status === 'submitting'}
                >
                  {magicLink.status === 'submitting'
                    ? 'Sending…'
                    : 'Send me a sign-in link'}
                </Button>
                {magicLink.status === 'error' && (
                  <p
                    role="alert"
                    className="mt-2 font-sans text-sm text-red-600"
                  >
                    {magicLink.message}
                  </p>
                )}
              </>
            )}
          </div>

          <div className="mt-6 border-t border-border pt-4 text-center font-sans text-xs text-fg-subtle">
            Trouble signing in?{' '}
            <a className="text-emerald-deep" href="/forgot-password">
              Reset your password
            </a>
            .
          </div>
        </CardContent>
      </Card>
    </section>
  );
}
