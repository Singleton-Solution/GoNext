#!/usr/bin/env node
/**
 * sync-schemas.mjs — mirror the Go-side hook schemas into this TS
 * package so both sides validate against byte-identical JSON.
 *
 * Why a sync script (and not symlinks, and not "TS imports the Go path
 * directly"):
 *
 *   - Symlinks don't survive pnpm publishes and break Windows CI.
 *   - Importing the Go path directly leaks the monorepo layout into the
 *     published TS artefact — consumers vending @gonext/hooks-schemas
 *     would have a phantom dependency on packages/go.
 *
 * The price is that humans have to run `pnpm sync-schemas` (or call the
 * script via CI before publish) whenever a Go-side schema changes. The
 * tradeoff is acceptable because:
 *
 *   - Schemas change rarely (each one represents a stable hook
 *     contract; breaking changes need a new hook name anyway).
 *   - A CI check that diffs the two directories catches drift before
 *     it ships. See .github/workflows/* for the wiring (TODO when CI
 *     ships).
 *
 * Invocation:
 *
 *   node packages/ts/hooks-schemas/scripts/sync-schemas.mjs [--check]
 *
 *   --check  Exit non-zero if the two directories differ. Used in CI.
 */
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const tsDir = path.join(here, '..', 'src', 'schemas');
const goDir = path.join(here, '..', '..', '..', 'go', 'hooks', 'schemas', 'schemas');

const args = new Set(process.argv.slice(2));
const checkOnly = args.has('--check');

async function listSchemas(dir) {
  const entries = await fs.readdir(dir);
  return entries.filter((name) => name.endsWith('.json')).sort();
}

async function readUtf8(file) {
  return fs.readFile(file, 'utf8');
}

async function main() {
  const [goNames, tsNames] = await Promise.all([
    listSchemas(goDir),
    listSchemas(tsDir).catch(() => []),
  ]);

  let drift = false;

  // Names present in one side but not the other → drift.
  const goSet = new Set(goNames);
  const tsSet = new Set(tsNames);
  const onlyInGo = goNames.filter((n) => !tsSet.has(n));
  const onlyInTs = tsNames.filter((n) => !goSet.has(n));
  if (onlyInGo.length > 0) {
    console.error(`schemas present only in Go: ${onlyInGo.join(', ')}`);
    drift = true;
  }
  if (onlyInTs.length > 0) {
    console.error(`schemas present only in TS: ${onlyInTs.join(', ')}`);
    drift = true;
  }

  // For names present in both: bytes must match.
  for (const name of goNames) {
    if (!tsSet.has(name)) continue;
    const [goBytes, tsBytes] = await Promise.all([
      readUtf8(path.join(goDir, name)),
      readUtf8(path.join(tsDir, name)),
    ]);
    if (goBytes !== tsBytes) {
      console.error(`schemas differ: ${name}`);
      drift = true;
    }
  }

  if (checkOnly) {
    if (drift) {
      console.error('drift detected; run `pnpm --filter @gonext/hooks-schemas sync-schemas` to resync.');
      process.exit(1);
    }
    console.log('hooks-schemas: in sync with Go side.');
    return;
  }

  // Sync mode: copy every Go-side file into the TS dir, removing any
  // stray TS-only files first so a renamed schema doesn't leave a
  // zombie behind.
  await fs.mkdir(tsDir, { recursive: true });
  for (const name of onlyInTs) {
    await fs.unlink(path.join(tsDir, name));
    console.log(`removed: ${name}`);
  }
  for (const name of goNames) {
    const src = path.join(goDir, name);
    const dst = path.join(tsDir, name);
    await fs.copyFile(src, dst);
  }
  console.log(`synced ${goNames.length} schemas from Go to TS.`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
