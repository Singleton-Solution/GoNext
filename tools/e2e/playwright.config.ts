import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for the GoNext e2e harness.
 *
 * Targets the docker-compose stack (see docker-compose.yml at repo root).
 * The base URL is environment driven so the same config works locally and
 * in CI. See docs/11-testing-ci.md §11 for the full e2e plan.
 *
 * Environment variables:
 *   E2E_BASE_URL   Base URL for the public web app. Defaults to http://localhost:3000.
 *   CI             Set by CI runners. Enables retries and disables headed mode.
 */

const isCI = !!process.env.CI;

export default defineConfig({
  testDir: './tests',
  // Fail the build on CI if you accidentally left test.only in the source.
  forbidOnly: isCI,
  // Retry on CI only — local runs should surface flakes immediately.
  retries: isCI ? 2 : 0,
  // Opt out of parallel mode by default; can be raised per project once
  // tenant isolation lands (issue #241 follow-up).
  workers: isCI ? 2 : undefined,
  // Reporters: list for local clarity, html for the artifact, github
  // annotations on CI.
  reporter: isCI
    ? [['github'], ['html', { open: 'never' }], ['list']]
    : [['list'], ['html', { open: 'never' }]],
  // 30s per test should be plenty for smoke checks; raise per-spec when
  // adding heavier journeys.
  timeout: 30_000,
  expect: {
    timeout: 5_000,
  },
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:3000',
    // Capture rich diagnostics only when a test fails or is retried.
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    // Reasonable defaults; specs can override.
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
    },
    {
      name: 'webkit',
      use: { ...devices['Desktop Safari'] },
    },
  ],
});
