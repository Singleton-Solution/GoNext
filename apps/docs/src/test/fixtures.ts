/**
 * Test fixture helpers for the docs site.
 *
 * Each test that touches the content walker spins up a temp directory,
 * writes a small markdown corpus, and points `listPages` / `buildNav` at
 * it. We use the OS temp dir + a random suffix so parallel test runs
 * never collide.
 */
import { promises as fs } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export interface FixtureFile {
  /** Path relative to the fixture root, e.g. `docs/00-overview.md`. */
  path: string;
  body: string;
}

export async function makeFixture(files: FixtureFile[]): Promise<string> {
  const root = join(tmpdir(), `gonext-docs-${Date.now()}-${Math.random().toString(36).slice(2)}`);
  await fs.mkdir(root, { recursive: true });
  for (const f of files) {
    const full = join(root, f.path);
    await fs.mkdir(join(full, '..'), { recursive: true });
    await fs.writeFile(full, f.body, 'utf8');
  }
  return root;
}

export async function cleanupFixture(root: string): Promise<void> {
  await fs.rm(root, { recursive: true, force: true });
}
