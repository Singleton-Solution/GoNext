#!/usr/bin/env node
/**
 * Sync the monorepo's `docs/` and `adr/` markdown trees into `apps/docs/content/`.
 *
 * Why a sync step instead of reading from `../../docs` at runtime:
 *  - Next.js's filesystem tracing is finicky about reaching outside the app
 *    directory; co-locating the content guarantees `generateStaticParams`
 *    and `outputFileTracingIncludes` cover it.
 *  - The build artifact is self-contained: someone can `cp -r .next` to a
 *    CDN and the source markdown is bundled for client-side search indexing.
 *  - It lets us preprocess in one place if we ever need to (rewrite relative
 *    links, strip auxiliary files, etc.).
 *
 * The script is intentionally dependency-free so it can run in the prebuild
 * hook before `pnpm install` has resolved transitive deps in CI cold-cache
 * scenarios. Stick to Node's built-in `node:fs` and `node:path`.
 */

import { promises as fs } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

const APP_ROOT = resolve(__dirname, '..');
const REPO_ROOT = resolve(APP_ROOT, '..', '..');
const CONTENT_DIR = join(APP_ROOT, 'content');

/**
 * Mappings from source tree to the synced content layout. The destination
 * folder names also drive the URL prefix (see app/docs/[...slug]/page.tsx).
 */
const SOURCES = [
  { src: join(REPO_ROOT, 'docs'), dest: join(CONTENT_DIR, 'docs') },
  { src: join(REPO_ROOT, 'adr'), dest: join(CONTENT_DIR, 'adr') },
];

/**
 * Recursively copy `*.md` files from src to dest, preserving subdirectories.
 * Returns the count of files copied — handy for the build log so a regression
 * (e.g. someone moves docs/ without updating this script) is obvious.
 */
async function copyMarkdown(src, dest) {
  let count = 0;
  let entries;
  try {
    entries = await fs.readdir(src, { withFileTypes: true });
  } catch (err) {
    if (err.code === 'ENOENT') {
      // Source directory does not exist (e.g. apps/docs cloned in isolation
      // for local hacking). Treat as empty rather than failing the build.
      console.warn(`[sync-content] source missing, skipped: ${src}`);
      return 0;
    }
    throw err;
  }

  await fs.mkdir(dest, { recursive: true });

  for (const entry of entries) {
    const srcPath = join(src, entry.name);
    const destPath = join(dest, entry.name);
    if (entry.isDirectory()) {
      count += await copyMarkdown(srcPath, destPath);
    } else if (entry.isFile() && /\.mdx?$/.test(entry.name)) {
      const buf = await fs.readFile(srcPath);
      await fs.writeFile(destPath, buf);
      count++;
    }
  }
  return count;
}

async function main() {
  // Clear out any previously synced content so deletions in the source tree
  // propagate. We do not touch the directory itself in case the user has it
  // open in an editor.
  try {
    await fs.rm(CONTENT_DIR, { recursive: true, force: true });
  } catch (err) {
    // ignore — `force: true` already swallows ENOENT.
  }
  await fs.mkdir(CONTENT_DIR, { recursive: true });

  let total = 0;
  for (const { src, dest } of SOURCES) {
    const n = await copyMarkdown(src, dest);
    console.log(`[sync-content] ${n} files: ${relative(REPO_ROOT, src)} -> ${relative(APP_ROOT, dest)}`);
    total += n;
  }
  console.log(`[sync-content] done. ${total} markdown files synced.`);
}

main().catch((err) => {
  console.error('[sync-content] failed:', err);
  process.exit(1);
});
