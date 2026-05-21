/**
 * Test helpers for the fresh-install smoke spec.
 *
 * Two pieces live here:
 *
 *   - `freshDatabase()` — wipes the e2e database between tests so each
 *     spec starts from a known state. Prefers `psql` (CI path) and
 *     falls back to a development-only REST endpoint
 *     (`/api/v1/dev/reset`) that the admin app exposes when
 *     `GONEXT_DEV_RESET=1` is set. Either path is gated behind
 *     `E2E_ALLOW_DESTRUCTIVE=1` so a misconfigured laptop never nukes
 *     a real database.
 *
 *   - `loginAs(...)` — POSTs to `/api/v1/auth/login` and returns the
 *     session cookie. We keep auth out of the browser when we just
 *     need a cookie because cookie-only auth is faster and lets us
 *     skip render churn between specs.
 *
 * These are deliberately framework-thin: the helpers take an
 * `APIRequestContext` so they can be reused from page-driven tests
 * and from pure-API setup hooks alike.
 */

import { execSync, spawnSync } from 'node:child_process';
import type { APIRequestContext } from '@playwright/test';

/**
 * Connection settings for the e2e database. These mirror the
 * docker-compose defaults; CI overrides via env vars when it boots a
 * GitHub-Actions Postgres service on a non-standard port.
 */
export interface DatabaseConfig {
  host: string;
  port: number;
  user: string;
  password: string;
  database: string;
}

export function databaseConfigFromEnv(): DatabaseConfig {
  return {
    host: process.env.E2E_PG_HOST ?? 'localhost',
    port: Number(process.env.E2E_PG_PORT ?? '5432'),
    user: process.env.E2E_PG_USER ?? 'gonext',
    password: process.env.E2E_PG_PASSWORD ?? 'gonext_dev_only',
    database: process.env.E2E_PG_DATABASE ?? 'gonext_dev',
  };
}

/**
 * Tables the smoke test touches. We TRUNCATE rather than drop so the
 * schema (migrations, sequences, enums) stays intact between runs —
 * `gonext init` is then free to re-seed the admin user.
 *
 * The list is conservative on purpose: it covers the surfaces the
 * fresh-install spec writes to. Other suites that grow into more
 * tables should extend it rather than truncating `public.*` blindly
 * — that has bitten us before by wiping the `schema_migrations`
 * bookkeeping row.
 */
const TRUNCATE_TABLES: readonly string[] = [
  'post_revisions',
  'post_autosaves',
  'posts',
  'permalinks',
  'sessions',
  'users',
];

function isPsqlAvailable(): boolean {
  // `command -v` is portable across Linux runners and macOS dev boxes;
  // `which` differs in subtle ways between distros.
  const result = spawnSync('sh', ['-c', 'command -v psql'], {
    stdio: 'ignore',
  });
  return result.status === 0;
}

function runPsql(cfg: DatabaseConfig, sql: string): void {
  // PGPASSWORD over -W so we don't surface the password in argv. The
  // env var is wiped from the spawned shell's environment on exit.
  execSync(
    [
      'psql',
      `-h ${cfg.host}`,
      `-p ${cfg.port}`,
      `-U ${cfg.user}`,
      `-d ${cfg.database}`,
      '-v ON_ERROR_STOP=1',
      '-1', // wrap the script in a single transaction
      `-c "${sql.replace(/"/g, '\\"')}"`,
    ].join(' '),
    {
      env: { ...process.env, PGPASSWORD: cfg.password },
      stdio: 'pipe',
    },
  );
}

async function tryDevResetEndpoint(
  request: APIRequestContext,
  apiBaseURL: string,
): Promise<boolean> {
  // The admin app exposes POST /api/v1/dev/reset when GONEXT_DEV_RESET=1
  // is set in its environment. We treat 200 and 204 as success; 404 is
  // the signal that the endpoint is intentionally disabled and we
  // should bubble up so the caller knows to bring the stack up with
  // the right flag.
  try {
    const response = await request.post(`${apiBaseURL}/api/v1/dev/reset`, {
      timeout: 5_000,
    });
    return response.status() === 200 || response.status() === 204;
  } catch {
    return false;
  }
}

/**
 * Reset the e2e database to a clean slate. Prefers `psql` if it's on
 * PATH (the CI path); otherwise falls back to the dev-only reset
 * endpoint described above. Throws if neither path is available so
 * misconfiguration surfaces immediately.
 *
 * The `E2E_ALLOW_DESTRUCTIVE=1` guard is a belt-and-braces measure:
 * even if someone accidentally points the e2e harness at a non-test
 * database, this helper will refuse to run.
 */
export async function freshDatabase(options: {
  request: APIRequestContext;
  apiBaseURL: string;
  cfg?: DatabaseConfig;
}): Promise<void> {
  if (process.env.E2E_ALLOW_DESTRUCTIVE !== '1') {
    throw new Error(
      'freshDatabase() refused to run: set E2E_ALLOW_DESTRUCTIVE=1 to confirm ' +
        'this is a throwaway database. Never set this against production.',
    );
  }

  const cfg = options.cfg ?? databaseConfigFromEnv();

  if (isPsqlAvailable()) {
    // Single TRUNCATE with CASCADE keeps the order independent of FK
    // direction. RESTART IDENTITY resets sequence counters so post IDs
    // don't keep climbing across CI runs.
    const sql = `TRUNCATE ${TRUNCATE_TABLES.join(', ')} RESTART IDENTITY CASCADE;`;
    runPsql(cfg, sql);
    return;
  }

  const ok = await tryDevResetEndpoint(options.request, options.apiBaseURL);
  if (!ok) {
    throw new Error(
      [
        'freshDatabase() could not reset the e2e database.',
        'Either install psql on the runner, or run the stack with',
        'GONEXT_DEV_RESET=1 so the admin exposes /api/v1/dev/reset.',
      ].join(' '),
    );
  }
}

/**
 * `gonext init` arguments. The CLI accepts these as flags once K1 lands;
 * until then `seedAdminUser()` below mimics the same effect by talking
 * directly to the database via psql.
 */
export interface InitArgs {
  adminEmail: string;
  adminPassword: string;
  siteName: string;
  siteUrl: string;
}

export const DEFAULT_INIT_ARGS: InitArgs = {
  adminEmail: 'e2e@example.com',
  adminPassword: 'CorrectHorseBatteryStaple-2026',
  siteName: 'E2E Test Site',
  siteUrl: 'http://localhost:3000',
};

/**
 * Run `gonext init` non-interactively. Returns true if the CLI is
 * present on PATH and exited 0; false otherwise so callers can fall
 * back to `seedAdminUser`.
 */
export function tryGonextInit(args: InitArgs): boolean {
  const cli = spawnSync(
    'gonext',
    [
      'init',
      '--admin-email',
      args.adminEmail,
      '--admin-password',
      args.adminPassword,
      '--site-name',
      args.siteName,
      '--site-url',
      args.siteUrl,
      '--non-interactive',
    ],
    { stdio: 'pipe' },
  );
  return cli.status === 0;
}

/**
 * Fallback admin seeder. Uses `psql` + the in-tree password hashing
 * helper exposed by `gonext util hash-password` (which the bench
 * fixtures already rely on). If neither binary is available, the
 * caller has bigger problems than this test — we surface a clear
 * error.
 *
 * The intent is: until #K1 lands, the smoke spec still works against
 * the docker-compose stack by inserting the admin row directly.
 */
export function seedAdminUser(
  args: InitArgs,
  cfg: DatabaseConfig = databaseConfigFromEnv(),
): void {
  if (!isPsqlAvailable()) {
    throw new Error('seedAdminUser() needs psql on PATH; none found');
  }
  // Use the dev-only `gonext util hash-password` helper to produce an
  // argon2id digest the API will accept. Falls back to a known-good
  // dev hash baked into the test fixture if the CLI is unavailable.
  const hashResult = spawnSync(
    'gonext',
    ['util', 'hash-password', args.adminPassword],
    { stdio: 'pipe', encoding: 'utf8' },
  );
  const passwordHash =
    hashResult.status === 0 && hashResult.stdout.trim().length > 0
      ? hashResult.stdout.trim()
      : process.env.E2E_ADMIN_PASSWORD_HASH ?? '';

  if (!passwordHash) {
    throw new Error(
      [
        'seedAdminUser() needs a password hash but the gonext CLI is not',
        'available. Set E2E_ADMIN_PASSWORD_HASH to a pre-computed argon2id',
        'hash of the password the test will use.',
      ].join(' '),
    );
  }

  const sql = [
    `INSERT INTO users (email, password_hash, display_name, role)`,
    `VALUES ('${args.adminEmail}', '${passwordHash}', 'E2E Admin', 'admin')`,
    `ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash;`,
  ].join(' ');
  runPsql(cfg, sql);
}

/**
 * Programmatic login. Returns the `Cookie:` header value the caller
 * can pass to `request.newContext({ extraHTTPHeaders })` or to a page
 * via `context.addCookies`. We deliberately do not bake assumptions
 * about cookie name here — the API may rename `gonext_session` later
 * and that's fine, the test only cares about round-tripping the
 * cookie back.
 */
export async function loginAs(
  request: APIRequestContext,
  apiBaseURL: string,
  email: string,
  password: string,
): Promise<string> {
  const response = await request.post(`${apiBaseURL}/api/v1/auth/login`, {
    data: { email, password },
    headers: { 'Content-Type': 'application/json' },
    timeout: 5_000,
  });
  if (response.status() !== 200 && response.status() !== 204) {
    const body = await response.text();
    throw new Error(
      `loginAs(${email}) failed: status=${response.status()} body=${body.slice(0, 200)}`,
    );
  }
  // Pick up every Set-Cookie the API sent us. The session cookie is
  // usually the only one but CSRF flows may attach a second.
  const rawCookies = response.headers()['set-cookie'] ?? '';
  if (!rawCookies) {
    throw new Error(
      'loginAs() received no Set-Cookie header — auth response is not ' +
        'cookie-based or the API changed shape.',
    );
  }
  // Convert "k=v; Path=/; HttpOnly" lines into a "k=v" Cookie header.
  return rawCookies
    .split(/\n|, (?=[A-Za-z0-9_-]+=)/)
    .map((line) => line.split(';')[0].trim())
    .filter((entry) => entry.length > 0)
    .join('; ');
}
