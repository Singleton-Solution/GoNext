/**
 * Playwright globalSetup for the fresh-install smoke spec.
 *
 * Runs once before any test starts. Two responsibilities:
 *
 *   1. Confirm the stack is up. If it isn't, fail fast with a clear
 *      message — the smoke spec is meaningless against a dead stack.
 *   2. Reset the e2e database and seed the admin user. Prefers
 *      `gonext init` (K1); falls back to `seedAdminUser()` via psql
 *      while the CLI is still in review.
 *
 * Activated only when `E2E_FRESH_INSTALL=1` is set so the existing
 * suite (smoke, a11y) keeps working without a database wipe between
 * runs. The smoke spec sets this for its own pnpm script entry.
 */
import { request } from '@playwright/test';
import {
  DEFAULT_INIT_ARGS,
  freshDatabase,
  seedAdminUser,
  tryGonextInit,
} from './lib/test-helpers';

export default async function globalSetup(): Promise<void> {
  if (process.env.E2E_FRESH_INSTALL !== '1') {
    return;
  }

  const webBaseURL = process.env.E2E_BASE_URL ?? 'http://localhost:3000';
  const apiBaseURL = process.env.E2E_API_BASE_URL ?? 'http://localhost:8080';

  // Probe both surfaces. We're explicit about which one is failing so
  // the diagnosis isn't "something is down". The web side is checked
  // first because if it's down nothing else matters; the API check
  // catches the case where the proxy is up but the API container has
  // crashed.
  const ctx = await request.newContext();
  try {
    const webResp = await ctx.get(webBaseURL, { timeout: 5_000 });
    if (webResp.status() >= 500) {
      throw new Error(
        `web at ${webBaseURL} returned ${webResp.status()}; bring the stack up with \`make up\``,
      );
    }
    const apiResp = await ctx.get(`${apiBaseURL}/healthz`, { timeout: 5_000 });
    if (apiResp.status() >= 500) {
      throw new Error(
        `api at ${apiBaseURL}/healthz returned ${apiResp.status()}`,
      );
    }

    await freshDatabase({ request: ctx, apiBaseURL });

    // Prefer the canonical path once it lands. The fallback keeps the
    // suite green while K1 is in review.
    const initOK = tryGonextInit(DEFAULT_INIT_ARGS);
    if (!initOK) {
      seedAdminUser(DEFAULT_INIT_ARGS);
    }
  } finally {
    await ctx.dispose();
  }
}
