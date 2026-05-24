/**
 * Tests for the SetupWizard client component.
 *
 * Coverage focus:
 *   - The wizard advances through steps when each step's local
 *     validation passes.
 *   - Weak passwords block the admin step (the gate is "Continue" being
 *     disabled, not a server round-trip).
 *   - On submit, the wizard POSTs the expected JSON shape to
 *     /api/v1/setup/install.
 *   - A 4xx response carries the operator back to the relevant step
 *     and renders the server's `message`.
 *   - A 200 response triggers a router.push to '/'.
 *
 * useRouter is mocked at the next/navigation seam so we can observe
 * the push call without spinning up a real Next runtime.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import SetupWizard, { isProbablyEmail, isProbablyURL } from './SetupWizard';

// Mock the navigation hook before importing the wizard. The mock
// returns a stable object whose push spy we can assert against.
const pushSpy = vi.fn();
vi.mock('next/navigation', () => ({
  useRouter: () => ({
    push: pushSpy,
    replace: vi.fn(),
    back: vi.fn(),
    forward: vi.fn(),
    refresh: vi.fn(),
    prefetch: vi.fn(),
  }),
}));

const VALID = {
  email: 'admin@example.com',
  password: 'correct-horse-battery-staple', // 28 chars
  siteName: 'Acme CMS',
  siteURL: 'https://acme.example.com',
};

// setup renders a fresh wizard. We do NOT install fake timers globally:
// vitest's waitFor uses setTimeout under the hood, and fake timers
// without explicit advancement leave it spinning forever. Tests that
// need to assert on the post-submit setTimeout(...) call use
// useFakeTimers locally after the awaitable assertions have settled.
function setup(): void {
  pushSpy.mockClear();
  render(
    <SetupWizard
      initialStatus={{ installation_completed: false, user_count: 0 }}
    />,
  );
}

beforeEach(() => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async () => {
    return new Response(
      JSON.stringify({ user_id: 'abc', expires_at: '2030-01-01T00:00:00Z' }),
      { status: 200, headers: { 'content-type': 'application/json' } },
    );
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

// ----- Helpers --------------------------------------------------------------

function clickButton(text: RegExp | string): void {
  const btn = screen.getByRole('button', { name: text });
  fireEvent.click(btn);
}

function type(label: RegExp | string, value: string): void {
  const input = screen.getByLabelText(label) as HTMLInputElement;
  fireEvent.change(input, { target: { value } });
}

// ----- Pure helpers ---------------------------------------------------------

describe('isProbablyEmail', () => {
  it('accepts canonical addresses', () => {
    expect(isProbablyEmail('a@b.co')).toBe(true);
    expect(isProbablyEmail('user.name+tag@sub.example.com')).toBe(true);
  });
  it('rejects malformed input', () => {
    for (const bad of ['', '@', 'a@', '@b.c', 'no-at', 'a b@c.d']) {
      expect(isProbablyEmail(bad)).toBe(false);
    }
  });
});

describe('isProbablyURL', () => {
  it('accepts http and https URLs', () => {
    expect(isProbablyURL('http://example.com')).toBe(true);
    expect(isProbablyURL('https://sub.example.com/path')).toBe(true);
  });
  it('rejects non-HTTP schemes and garbage', () => {
    for (const bad of ['', 'ftp://example.com', 'not a url', 'example.com']) {
      expect(isProbablyURL(bad)).toBe(false);
    }
  });
});

// ----- Wizard flow ----------------------------------------------------------

describe('SetupWizard — step navigation', () => {
  it('starts on the Welcome step', () => {
    setup();
    expect(screen.getByText(/Welcome to GoNext/i)).toBeTruthy();
  });

  it('advances welcome → admin → site → review → done on the happy path', async () => {
    setup();
    clickButton(/Begin/i);
    expect(screen.getByRole('heading', { name: /Administrator/i })).toBeTruthy();

    type(/Email/i, VALID.email);
    type(/Password/i, VALID.password);
    clickButton(/Continue/i);

    expect(screen.getByRole('heading', { name: /^Site$/i })).toBeTruthy();
    type(/Site name/i, VALID.siteName);
    type(/Site URL/i, VALID.siteURL);
    clickButton(/Continue/i);

    expect(screen.getByRole('heading', { name: /Review/i })).toBeTruthy();
    expect(screen.getByText(VALID.email)).toBeTruthy();
    expect(screen.getByText(VALID.siteName)).toBeTruthy();
    expect(screen.getByText(VALID.siteURL)).toBeTruthy();

    clickButton(/Confirm install/i);
    // POST fires.
    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledTimes(1);
    });
    // The wizard moves to the success state.
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /You are in/i })).toBeTruthy();
    });
    // After ~1.5s the wizard schedules a router.push. Wait it out on
    // real timers — the timeout is short enough that the suite still
    // finishes well under the default 5s budget.
    await waitFor(
      () => {
        expect(pushSpy).toHaveBeenCalledWith('/');
      },
      { timeout: 3000 },
    );
  });
});

describe('SetupWizard — admin step validation', () => {
  it('disables Continue when the password is below the 12-char floor', () => {
    setup();
    clickButton(/Begin/i);
    type(/Email/i, VALID.email);
    type(/Password/i, 'short'); // 5 chars
    const cont = screen.getByRole('button', { name: /Continue/i });
    expect(cont.hasAttribute('disabled')).toBe(true);
  });

  it('disables Continue when the email is malformed', () => {
    setup();
    clickButton(/Begin/i);
    type(/Email/i, 'not-an-email');
    type(/Password/i, VALID.password);
    const cont = screen.getByRole('button', { name: /Continue/i });
    expect(cont.hasAttribute('disabled')).toBe(true);
  });

  it('enables Continue when both fields meet the local rules', () => {
    setup();
    clickButton(/Begin/i);
    type(/Email/i, VALID.email);
    type(/Password/i, VALID.password);
    const cont = screen.getByRole('button', { name: /Continue/i });
    expect(cont.hasAttribute('disabled')).toBe(false);
  });
});

describe('SetupWizard — submit', () => {
  it('posts the snake_case install payload to /api/v1/setup/install', async () => {
    setup();
    clickButton(/Begin/i);
    type(/Email/i, VALID.email);
    type(/Password/i, VALID.password);
    clickButton(/Continue/i);
    type(/Site name/i, VALID.siteName);
    type(/Site URL/i, VALID.siteURL);
    clickButton(/Continue/i);
    clickButton(/Confirm install/i);

    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalled();
    });
    const [url, init] = (globalThis.fetch as unknown as { mock: { calls: unknown[][] } })
      .mock.calls[0]! as [string, RequestInit];
    expect(url).toContain('/api/v1/setup/install');
    expect(init.method).toBe('POST');
    expect(init.credentials).toBe('include');
    const body = JSON.parse(init.body as string);
    expect(body).toEqual({
      admin_email: VALID.email,
      admin_password: VALID.password,
      site_name: VALID.siteName,
      site_url: VALID.siteURL,
    });
  });

  it('surfaces a server error message and returns to the admin step on weak_password', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementationOnce(async () => {
      return new Response(
        JSON.stringify({
          code: 'weak_password',
          message: 'Password must be at least 12 characters.',
        }),
        { status: 400, headers: { 'content-type': 'application/json' } },
      );
    });
    setup();
    clickButton(/Begin/i);
    type(/Email/i, VALID.email);
    type(/Password/i, VALID.password);
    clickButton(/Continue/i);
    type(/Site name/i, VALID.siteName);
    type(/Site URL/i, VALID.siteURL);
    clickButton(/Continue/i);
    clickButton(/Confirm install/i);

    await waitFor(() => {
      // Should bounce back to the admin step.
      expect(screen.getByRole('heading', { name: /Administrator/i })).toBeTruthy();
    });
    expect(screen.getByText(/Password must be at least 12 characters/i)).toBeTruthy();
    expect(pushSpy).not.toHaveBeenCalled();
  });
});
