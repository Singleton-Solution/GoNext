'use client';

/**
 * Login — form skeleton.
 *
 * This is intentionally not wired to the API yet. The real auth flow
 * (CSRF token fetch, POST /v1/sessions, cookie handling, error states)
 * lands with the auth UI issue. For now we render a valid, accessible form
 * structure so the IA includes a login surface and the route exists.
 *
 * Marked as a Client Component because the form's onSubmit handler must run
 * on the client (React requires event handlers in Client Components).
 */
import type { ReactElement } from 'react';

export default function LoginPage(): ReactElement {
  return (
    <section className="login-card">
      <h1>Sign in</h1>
      <p className="muted">Use your GoNext admin credentials to continue.</p>
      <form
        // No action yet — submission is a no-op until the auth wire-up.
        onSubmit={(event) => event.preventDefault()}
        noValidate
      >
        <div className="form-field">
          <label htmlFor="email">Email</label>
          <input
            id="email"
            name="email"
            type="email"
            autoComplete="username"
            required
          />
        </div>
        <div className="form-field">
          <label htmlFor="password">Password</label>
          <input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
          />
        </div>
        <button type="submit" className="btn-primary">
          Sign in
        </button>
      </form>
    </section>
  );
}
