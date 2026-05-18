#!/usr/bin/env node
/**
 * @gonext/bundle-budget CLI.
 *
 * Usage:
 *   node bin/check.js --app admin --next apps/admin/.next \
 *     [--budgets tools/bundle-budget/budgets.yaml] \
 *     [--summary $GITHUB_STEP_SUMMARY]
 *
 * Exits with 0 on success, 1 on budget violation, 2 on usage / manifest
 * errors. The exit codes are deliberately distinct so a CI matrix can tell
 * "the gate failed" apart from "the gate couldn't run". When `--summary` is
 * given, the rendered markdown is appended to that file (GitHub's step
 * summary mechanism); otherwise it's printed to stdout so a developer
 * running this locally still sees the table.
 *
 * This file is a small wrapper — all the logic lives in src/. It uses tsx
 * via the loader flag so we can keep src/ as `.ts` and avoid a separate
 * build step for what is essentially a CI utility.
 */
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

function parseArgs(argv) {
  const args = { app: null, next: null, budgets: null, summary: null };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--app') args.app = argv[++i];
    else if (a === '--next') args.next = argv[++i];
    else if (a === '--budgets') args.budgets = argv[++i];
    else if (a === '--summary') args.summary = argv[++i];
    else if (a === '-h' || a === '--help') {
      printHelp();
      process.exit(0);
    } else {
      console.error(`Unknown argument: ${a}`);
      printHelp();
      process.exit(2);
    }
  }
  return args;
}

function printHelp() {
  console.error(`Usage: gonext-bundle-budget [options]

Options:
  --app <name>      Workspace name without the @gonext/ prefix (admin | web)
  --next <dir>      Path to the .next build directory
  --budgets <file>  Path to budgets.yaml (defaults to the one alongside this CLI)
  --summary <file>  Optional path to append markdown summary to (e.g. \$GITHUB_STEP_SUMMARY)
  -h, --help        Show this help

Exit codes:
  0  All budgets satisfied
  1  One or more routes exceeded their budget
  2  Usage or manifest error (could not run)
`);
}

async function main() {
  const args = parseArgs(process.argv.slice(2));

  if (!args.app || !args.next) {
    console.error('Error: --app and --next are required.');
    printHelp();
    process.exit(2);
  }

  const budgetsPath =
    args.budgets ?? path.resolve(__dirname, '..', 'budgets.yaml');

  // Dynamic import the TS source via the URL form so we work both when run
  // from the repo (with tsx) and when published (after a hypothetical build).
  // The path is relative to this file's location.
  const srcEntry = path.resolve(__dirname, '..', 'src', 'check.ts');
  const { runCheck } = await import(pathToFileURL(srcEntry).href);

  let result;
  try {
    result = runCheck({
      app: args.app,
      nextDir: args.next,
      budgetsPath,
    });
  } catch (err) {
    console.error(`bundle-budget: ${err.message ?? err}`);
    process.exit(2);
  }

  const output = result.summaryMarkdown +
    (result.failureMarkdown ? '\n' + result.failureMarkdown : '');

  if (args.summary) {
    fs.appendFileSync(args.summary, output + '\n');
  }
  // Always echo to stdout so local runs see the table.
  process.stdout.write(output + '\n');

  if (!result.passed) {
    console.error(
      `bundle-budget: ${result.app} exceeded budget on ${
        result.routes.filter((r) => r.violations.length > 0).length
      } route(s).`,
    );
    process.exit(1);
  }
  process.exit(0);
}

main().catch((err) => {
  console.error(err);
  process.exit(2);
});
