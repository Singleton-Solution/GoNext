/**
 * End-to-end test for the top-level `runCheck` orchestrator.
 *
 * Mounts a real `.next` directory and a real budgets.yaml in a tmpdir, then
 * exercises the orchestrator from input bytes through markdown output. This
 * is the smallest case where the CLI's contract is observable.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { runCheck } from '../src/check.ts';

function setup(): { nextDir: string; budgetsPath: string; cleanup: () => void } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bb-e2e-'));
  const nextDir = path.join(dir, '.next');
  fs.mkdirSync(path.join(nextDir, 'static', 'chunks'), { recursive: true });
  fs.mkdirSync(path.join(nextDir, 'static', 'css'), { recursive: true });

  // Predictable payloads — large enough that the gzipped size lands above
  // 1 KB so rounding to one decimal still leaves a comparable number.
  fs.writeFileSync(
    path.join(nextDir, 'static', 'chunks', 'app.js'),
    'console.log("hi");\n'.repeat(2_000),
  );
  fs.writeFileSync(
    path.join(nextDir, 'static', 'css', 'app.css'),
    'body{color:red}'.repeat(500),
  );
  fs.writeFileSync(
    path.join(nextDir, 'build-manifest.json'),
    JSON.stringify({
      pages: {
        '/': ['static/chunks/app.js', 'static/css/app.css'],
      },
    }),
  );

  const budgetsPath = path.join(dir, 'budgets.yaml');
  fs.writeFileSync(
    budgetsPath,
    `
global:
  maxTransferKb: 1000
routes:
  - app: admin
    route: /
    maxJsKb: 250
    maxCssKb: 40
`,
  );

  return {
    nextDir,
    budgetsPath,
    cleanup: () => fs.rmSync(dir, { recursive: true, force: true }),
  };
}

describe('runCheck', () => {
  let env: ReturnType<typeof setup>;

  beforeEach(() => {
    env = setup();
  });

  afterEach(() => {
    env.cleanup();
  });

  it('passes when the build fits inside the budget', () => {
    const result = runCheck({
      app: 'admin',
      nextDir: env.nextDir,
      budgetsPath: env.budgetsPath,
    });
    expect(result.passed).toBe(true);
    expect(result.failureMarkdown).toBe('');
    expect(result.summaryMarkdown).toContain('admin');
    expect(result.summaryMarkdown).toContain('OK');
  });

  it('fails and renders a failure diff when a budget is exceeded', () => {
    // Tighten the budget so the same fixture now blows through.
    fs.writeFileSync(
      env.budgetsPath,
      `
global:
  maxTransferKb: 1000
routes:
  - app: admin
    route: /
    maxJsKb: 0.1
    maxCssKb: 0.1
`,
    );
    const result = runCheck({
      app: 'admin',
      nextDir: env.nextDir,
      budgetsPath: env.budgetsPath,
    });
    expect(result.passed).toBe(false);
    expect(result.failureMarkdown).toContain('Budget violations');
    expect(result.summaryMarkdown).toContain('FAIL');
  });
});
