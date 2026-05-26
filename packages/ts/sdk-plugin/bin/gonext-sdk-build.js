#!/usr/bin/env node
/**
 * gonext-sdk-build — TypeScript plugin build pipeline for GoNext.
 *
 * Wraps esbuild (bundler/transpiler) + Javy (Shopify's JS→WASM
 * compiler) into a single one-shot command:
 *
 *     1. esbuild bundles `src/index.ts` (plus its workspace imports
 *        from @gonext/sdk-plugin) into a single `dist/plugin.js`.
 *     2. `javy compile dist/plugin.js -o dist/plugin.wasm` produces
 *        the WASM module.
 *     3. The manifest (manifest.json or manifest.ts default-exporting
 *        a `Manifest`) is validated against the same shape the host's
 *        installer enforces. The result is written to
 *        `dist/manifest.json` next to the wasm.
 *
 * The CLI is intentionally light: it does NOT bundle the .gnplugin
 * zip or invoke signing — those concerns are owned by
 * `gonext plugin sign` and the in-tree marketplace publisher. This
 * binary's sole job is "produce a valid plugin.wasm + manifest.json".
 *
 * Flags
 *   --src       Source entry. Default: src/index.ts.
 *   --out       Output directory. Default: dist.
 *   --manifest  Path to manifest source (.json or .ts). Default: manifest.json.
 *   --javy      Path to the Javy binary. Default: `javy` on PATH.
 *   --skip-wasm Skip the Javy step (manifest validation only). Useful
 *               in CI environments that don't have Javy installed; the
 *               build still surfaces every TypeScript/manifest issue.
 *   --help      Print this message.
 *
 * Exit codes
 *   0 — build succeeded.
 *   1 — bundling, manifest validation, or Javy compile failed.
 *   2 — usage error.
 *
 * Why JS, not TS: the binary runs as `npx gonext-sdk-build` in
 * downstream plugin repos that don't have a TypeScript loader wired
 * in. Keeping the wrapper as plain Node JS means it works against any
 * Node 22+ install without an additional toolchain dependency.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import process from 'node:process';

const __dirname = dirname(fileURLToPath(import.meta.url));

function usage() {
  return [
    'gonext-sdk-build — TypeScript plugin build pipeline (esbuild + Javy)',
    '',
    'Usage:',
    '  gonext-sdk-build [flags]',
    '',
    'Flags:',
    '  --src <path>       Source entry (default: src/index.ts)',
    '  --out <dir>        Output directory (default: dist)',
    '  --manifest <path>  Manifest source (.json or .ts; default: manifest.json)',
    '  --javy <bin>       Path to the Javy binary (default: javy on PATH)',
    '  --skip-wasm        Skip the Javy step (manifest+bundle only)',
    '  --help             Show this message',
  ].join('\n');
}

function parseArgs(argv) {
  const out = {
    src: 'src/index.ts',
    out: 'dist',
    manifest: 'manifest.json',
    javy: 'javy',
    skipWasm: false,
    help: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    switch (arg) {
      case '--src':
        out.src = argv[++i] ?? '';
        break;
      case '--out':
        out.out = argv[++i] ?? '';
        break;
      case '--manifest':
        out.manifest = argv[++i] ?? '';
        break;
      case '--javy':
        out.javy = argv[++i] ?? '';
        break;
      case '--skip-wasm':
        out.skipWasm = true;
        break;
      case '--help':
      case '-h':
        out.help = true;
        break;
      default:
        process.stderr.write(`gonext-sdk-build: unknown flag ${arg}\n${usage()}\n`);
        process.exit(2);
    }
  }
  return out;
}

/**
 * Bundle the plugin source with esbuild. Output is a single
 * ES2020-targeted JavaScript file Javy can ingest.
 *
 * We bundle (rather than ship raw source) because:
 *   1. Javy expects exactly one input file.
 *   2. The plugin imports from @gonext/sdk-plugin under node_modules;
 *      Javy doesn't resolve node_modules itself.
 *   3. esbuild handles .ts -> .js, including the .ts-extension imports
 *      the SDK uses internally.
 *
 * We deliberately don't run a typecheck here — that's `pnpm typecheck`
 * (or `tsc --noEmit`) in the user's own scripts. This build step is the
 * "produce the artefact" lane; typing is the "validate the code" lane.
 */
async function runBundler(srcPath, outJsPath) {
  let esbuild;
  try {
    esbuild = await import('esbuild');
  } catch (err) {
    throw new Error(
      `esbuild is required (npm install esbuild). Original error: ${err.message}`,
    );
  }
  await esbuild.build({
    entryPoints: [srcPath],
    outfile: outJsPath,
    bundle: true,
    format: 'esm',
    target: 'es2020',
    platform: 'neutral',
    // Javy's runtime exposes the host imports as globals (gn_*), so we
    // don't need to inject any polyfills here. The neutral platform
    // setting tells esbuild to neither emit Node nor browser shims.
    logLevel: 'info',
  });
}

function runJavy(javyBin, jsPath, wasmPath) {
  // Javy CLI: `javy compile <input.js> -o <output.wasm>`.
  // See https://github.com/bytecodealliance/javy for docs.
  const proc = spawnSync(javyBin, ['compile', jsPath, '-o', wasmPath], {
    stdio: 'inherit',
    shell: process.platform === 'win32',
  });
  if (proc.error && proc.error.code === 'ENOENT') {
    throw new Error(
      `javy binary not found (looked for ${javyBin}). Install Javy or pass --javy <path>; ` +
        `see https://github.com/bytecodealliance/javy.`,
    );
  }
  if (proc.status !== 0) {
    throw new Error('javy compile exited with non-zero status');
  }
}

async function loadManifest(manifestPath) {
  const ext = manifestPath.split('.').pop()?.toLowerCase();
  if (ext === 'json') {
    const raw = await readFile(manifestPath, 'utf8');
    const parsed = JSON.parse(raw);
    // Drop the apiVersion if present — buildManifest re-injects it.
    delete parsed.apiVersion;
    return parsed;
  }
  if (ext === 'ts' || ext === 'js' || ext === 'mjs') {
    const url = pathToFileURL(resolve(manifestPath));
    // Dynamic import — Node's loader handles .js/.mjs natively; .ts
    // requires ts-node or a similar shim that the consuming repo
    // wires in. We don't take a hard dependency here.
    const mod = await import(url.href);
    const input = mod.default ?? mod.manifest ?? mod;
    if (input && typeof input === 'object' && 'apiVersion' in input) {
      delete /** @type {any} */ (input).apiVersion;
    }
    return input;
  }
  throw new Error(`unsupported manifest extension: ${manifestPath}`);
}

async function validateAndWriteManifest(manifestSrc, outManifestPath) {
  // We import the sibling builder rather than the published package
  // so the CLI works against an unbuilt workspace checkout.
  const builderURL = pathToFileURL(join(__dirname, '..', 'src', 'manifest.ts')).href;
  let buildManifest;
  let manifestToJSON;
  try {
    ({ buildManifest, manifestToJSON } = await import(builderURL));
  } catch (err) {
    // When the manifest builder lives in a published package, callers
    // resolve it via `@gonext/sdk-plugin`. Fall back to that path.
    try {
      ({ buildManifest, manifestToJSON } = await import('@gonext/sdk-plugin'));
    } catch (innerErr) {
      throw new Error(
        `unable to load manifest builder (${err.message}; fallback: ${innerErr.message})`,
      );
    }
  }
  const input = await loadManifest(manifestSrc);
  const built = buildManifest(input);
  writeFileSync(outManifestPath, manifestToJSON(built), 'utf8');
  return built;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) {
    process.stdout.write(usage() + '\n');
    process.exit(0);
  }

  const cwd = process.cwd();
  const srcPath = isAbsolute(args.src) ? args.src : join(cwd, args.src);
  const outDir = isAbsolute(args.out) ? args.out : join(cwd, args.out);
  const manifestSrc = isAbsolute(args.manifest)
    ? args.manifest
    : join(cwd, args.manifest);

  if (!existsSync(srcPath)) {
    process.stderr.write(`gonext-sdk-build: source not found: ${srcPath}\n`);
    process.exit(1);
  }
  if (!existsSync(manifestSrc)) {
    process.stderr.write(`gonext-sdk-build: manifest not found: ${manifestSrc}\n`);
    process.exit(1);
  }

  mkdirSync(outDir, { recursive: true });

  const outManifest = join(outDir, 'manifest.json');
  process.stdout.write('gonext-sdk-build: validating manifest\n');
  let built;
  try {
    built = await validateAndWriteManifest(manifestSrc, outManifest);
  } catch (err) {
    process.stderr.write(`gonext-sdk-build: manifest validation failed:\n${err.message}\n`);
    if (err && err.issues) {
      for (const i of err.issues) {
        process.stderr.write(`  - ${i.path}: ${i.message}\n`);
      }
    }
    process.exit(1);
  }

  // Bundle: src/index.ts (+ workspace deps) -> dist/plugin.js
  const bundledJs = join(outDir, 'plugin.js');
  process.stdout.write(`gonext-sdk-build: bundling ${srcPath} -> ${bundledJs}\n`);
  try {
    await runBundler(srcPath, bundledJs);
  } catch (err) {
    process.stderr.write(`gonext-sdk-build: bundle failed: ${err.message}\n`);
    process.exit(1);
  }

  if (args.skipWasm) {
    process.stdout.write(
      `gonext-sdk-build: --skip-wasm set; manifest at ${outManifest}, bundle at ${bundledJs}\n`,
    );
    process.exit(0);
  }

  const outWasm = join(outDir, built.entry || 'plugin.wasm');
  mkdirSync(dirname(outWasm), { recursive: true });
  process.stdout.write(`gonext-sdk-build: compiling JS -> ${outWasm} via Javy\n`);
  try {
    runJavy(args.javy, bundledJs, outWasm);
  } catch (err) {
    process.stderr.write(`gonext-sdk-build: javy failed: ${err.message}\n`);
    process.exit(1);
  }

  process.stdout.write(
    `gonext-sdk-build: built ${outWasm} + ${outManifest}\n`,
  );
}

main().catch((err) => {
  process.stderr.write(`gonext-sdk-build: unexpected error: ${err?.stack ?? err}\n`);
  process.exit(1);
});
