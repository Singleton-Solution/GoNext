/**
 * Tests for the StatusReport-to-card heuristics.
 *
 * The deriver functions are pure — given a section payload they return
 * a tone + summary + row list. Testing them at the function level
 * means each "yellow vs red" decision is one assertion, with no React
 * rendering overhead.
 */
import { describe, expect, it } from 'vitest';
import {
  deriveBuildInfo,
  deriveDatabase,
  deriveDisk,
  deriveMigrations,
  derivePlugins,
  deriveQueue,
  deriveRedis,
  deriveTheme,
  formatBytes,
  redactDiagnostic,
} from './derive';
import type { StatusReport } from './types';

const baseReport: StatusReport = {
  version: 'v1.2.3',
  commit: 'abc123def456789',
  build_date: '2026-05-17T00:00:00Z',
  go_version: 'go1.25.0',
  os: 'linux',
  arch: 'amd64',
  generated: '2026-05-17T12:00:00Z',
  database: { ok: true, version: 'PostgreSQL 16', max_conns: 10, in_use: 2, idle: 8, response_time_ms: 1 },
  redis: { ok: true, version: '7.2.4', response_time_ms: 1 },
  migrations: { current_version: 42, dirty: false, total_count: 42 },
  queues: [],
  theme: { active_name: 'gn-pro', version: 'v1', parts_count: 4, templates_count: 3 },
  plugins: { installed: 5, active: 4, errored: 0 },
  disk: { theme_dir_bytes: 12345, media_dir_bytes: 67890000 },
};

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [500, '500 B'],
    [1024, '1.0 KB'],
    [1536, '1.5 KB'],
    [1024 * 1024, '1.0 MB'],
    [10 * 1024 * 1024, '10 MB'],
    [1024 * 1024 * 1024, '1.0 GB'],
  ])('formats %d as %s', (input, want) => {
    expect(formatBytes(input)).toBe(want);
  });

  it('returns em-dash for invalid inputs', () => {
    expect(formatBytes(-1)).toBe('—');
    expect(formatBytes(Number.NaN)).toBe('—');
  });
});

describe('deriveDatabase', () => {
  it('is ok when pool utilization is low', () => {
    const out = deriveDatabase(baseReport.database);
    expect(out.tone).toBe('ok');
    expect(out.rows.find((r) => r.label === 'Version')?.value).toBe('PostgreSQL 16');
  });

  it('warns when in_use >= 80% of max_conns', () => {
    const out = deriveDatabase({ ...baseReport.database, in_use: 8 });
    expect(out.tone).toBe('warn');
    expect(out.summary).toMatch(/80%|scaling/i);
  });

  it('is error when ok=false', () => {
    const out = deriveDatabase({ ...baseReport.database, ok: false, error: 'ping timeout' });
    expect(out.tone).toBe('error');
    expect(out.errorMessage).toBe('ping timeout');
  });

  it('is unknown when source not configured', () => {
    const out = deriveDatabase({
      ok: false, max_conns: 0, in_use: 0, idle: 0, response_time_ms: 0,
      error: 'source not configured',
    });
    expect(out.tone).toBe('unknown');
  });
});

describe('deriveRedis', () => {
  it('is ok on healthy ping', () => {
    expect(deriveRedis(baseReport.redis).tone).toBe('ok');
  });
  it('is error on ping failure', () => {
    expect(deriveRedis({ ok: false, response_time_ms: 0, error: 'connection refused' }).tone).toBe('error');
  });
});

describe('deriveMigrations', () => {
  it('is ok when current == total', () => {
    expect(deriveMigrations(baseReport.migrations).tone).toBe('ok');
  });

  it('warns when migrations are pending', () => {
    const out = deriveMigrations({ ...baseReport.migrations, current_version: 40, total_count: 42 });
    expect(out.tone).toBe('warn');
    expect(out.summary).toMatch(/2 migration/);
  });

  it('is error when dirty=true', () => {
    const out = deriveMigrations({ ...baseReport.migrations, dirty: true });
    expect(out.tone).toBe('error');
    expect(out.errorMessage).toMatch(/dirty/);
  });
});

describe('derivePlugins', () => {
  it('is ok with zero errored', () => {
    expect(derivePlugins(baseReport.plugins).tone).toBe('ok');
  });

  it('is error when any plugin is in errored state', () => {
    const out = derivePlugins({ installed: 5, active: 3, errored: 1 });
    expect(out.tone).toBe('error');
    expect(out.summary).toMatch(/errored state/);
  });
});

describe('deriveTheme', () => {
  it('is ok for an active theme', () => {
    const out = deriveTheme(baseReport.theme);
    expect(out.tone).toBe('ok');
    expect(out.rows.find((r) => r.label === 'Parts')?.value).toBe('4');
  });

  it('is error when theme is missing', () => {
    const out = deriveTheme({ active_name: '', parts_count: 0, templates_count: 0, error: 'no active theme' });
    expect(out.tone).toBe('error');
  });
});

describe('deriveDisk', () => {
  it('formats bytes for the rows', () => {
    const out = deriveDisk(baseReport.disk);
    expect(out.tone).toBe('ok');
    expect(out.rows.find((r) => r.label === 'Theme dir')?.value).toBe('12 KB');
  });
});

describe('deriveQueue', () => {
  it('is ok on quiet queues', () => {
    const out = deriveQueue({ name: 'critical', pending: 0, active: 0, processed_24h: 100, failed_24h: 0 });
    expect(out.tone).toBe('ok');
  });

  it('warns at a >5% failure rate', () => {
    const out = deriveQueue({ name: 'webhook', pending: 0, active: 0, processed_24h: 100, failed_24h: 10 });
    expect(out.tone).toBe('warn');
    expect(out.summary).toMatch(/failure rate/);
  });

  it('warns on a deep pending backlog', () => {
    const out = deriveQueue({ name: 'media', pending: 5000, active: 1, processed_24h: 100, failed_24h: 0 });
    expect(out.tone).toBe('warn');
    expect(out.summary).toMatch(/backed up/);
  });

  it('is error when the inspector returns a per-queue error', () => {
    const out = deriveQueue({
      name: 'missing', pending: 0, active: 0, processed_24h: 0, failed_24h: 0,
      error: 'queue not found',
    });
    expect(out.tone).toBe('error');
  });
});

describe('deriveBuildInfo', () => {
  it('shortens long commit SHAs', () => {
    const out = deriveBuildInfo(baseReport);
    expect(out.rows.find((r) => r.label === 'Commit')?.value).toBe('abc123def456');
  });
});

describe('redactDiagnostic', () => {
  it('returns the full report as JSON', () => {
    const text = redactDiagnostic(baseReport);
    const parsed = JSON.parse(text) as StatusReport;
    expect(parsed.version).toBe(baseReport.version);
    expect(parsed.database.max_conns).toBe(10);
  });
});
