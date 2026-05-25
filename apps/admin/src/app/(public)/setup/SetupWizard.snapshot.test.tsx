/**
 * Snapshot coverage for the restyled SetupWizard steps.
 *
 * The brand-restyle pulls every step under the Living-Systems treatment
 * (cream paper card, Archivo headlines with italic-accent rule, Lucide
 * icons, Button + Input primitives). To keep that visual contract
 * regression-tested without coupling to specific class names, each
 * step is rendered into its own snapshot file. A snapshot drift means
 * one of two things — the brand surface changed (regenerate with
 * `vitest -u` and review the diff), or a refactor leaked an
 * unintended structural change.
 *
 * We exercise all five steps:
 *   1. welcome  — initial render
 *   2. admin    — after Begin
 *   3. site     — after admin form passes validation
 *   4. review   — after site form passes validation
 *   5. done     — after a successful install POST
 *
 * Behavioural assertions for each transition already live in
 * SetupWizard.test.tsx; this file is intentionally render-only.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import SetupWizard from './SetupWizard';

vi.mock('next/navigation', () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    forward: vi.fn(),
    refresh: vi.fn(),
    prefetch: vi.fn(),
  }),
}));

const VALID = {
  email: 'admin@example.com',
  password: 'correct-horse-battery-staple',
  siteName: 'Acme CMS',
  siteURL: 'https://acme.example.com',
};

function renderWizard(): HTMLElement {
  const { container } = render(
    <SetupWizard
      initialStatus={{ installation_completed: false, user_count: 0 }}
    />,
  );
  return container;
}

function clickButton(name: RegExp | string): void {
  fireEvent.click(screen.getByRole('button', { name }));
}

function type(label: RegExp | string, value: string): void {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
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

describe('SetupWizard — snapshots per step', () => {
  it('matches the Welcome step', () => {
    const container = renderWizard();
    expect(container).toMatchSnapshot();
  });

  it('matches the Admin step', () => {
    const container = renderWizard();
    clickButton(/Begin/i);
    expect(container).toMatchSnapshot();
  });

  it('matches the Site step', () => {
    const container = renderWizard();
    clickButton(/Begin/i);
    type(/Email/i, VALID.email);
    type(/Password/i, VALID.password);
    clickButton(/Continue/i);
    expect(container).toMatchSnapshot();
  });

  it('matches the Review step', () => {
    const container = renderWizard();
    clickButton(/Begin/i);
    type(/Email/i, VALID.email);
    type(/Password/i, VALID.password);
    clickButton(/Continue/i);
    type(/Site name/i, VALID.siteName);
    type(/Site URL/i, VALID.siteURL);
    clickButton(/Continue/i);
    expect(container).toMatchSnapshot();
  });

  it('matches the Done step after a successful install', async () => {
    const container = renderWizard();
    clickButton(/Begin/i);
    type(/Email/i, VALID.email);
    type(/Password/i, VALID.password);
    clickButton(/Continue/i);
    type(/Site name/i, VALID.siteName);
    type(/Site URL/i, VALID.siteURL);
    clickButton(/Continue/i);
    await act(async () => {
      clickButton(/Confirm install/i);
    });
    await waitFor(() => {
      expect(
        screen.getByRole('heading', { name: /You are in/i }),
      ).toBeTruthy();
    });
    expect(container).toMatchSnapshot();
  });
});
