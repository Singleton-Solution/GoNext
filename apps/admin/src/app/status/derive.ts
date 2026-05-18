/**
 * Pure helpers that turn raw StatusReport values into card-ready data.
 *
 * Keeping the tone heuristics in pure functions means tests don't need
 * to render React to verify the "yellow vs red" decisions — they can
 * call the function with a fixture and assert the output. Each helper
 * returns the data its corresponding card consumes (tone + summary +
 * row list); the card stays presentation-only.
 *
 * The thresholds are deliberately conservative; the goal is "operator
 * notices something is off before the user does", not "page goes red
 * the first time queue depth hits 1".
 */
import type {
  DatabaseStatus,
  DiskStatus,
  MigrationsStatus,
  PluginsStatus,
  QueueStatus,
  RedisStatus,
  StatusReport,
  StatusTone,
  ThemeStatus,
} from './types';

export interface CardData {
  tone: StatusTone;
  summary?: string;
  errorMessage?: string;
  rows: { label: string; value: string }[];
}

/**
 * Format a byte count as a human-readable string with two significant
 * digits past the unit boundary. Re-implemented here rather than
 * pulled from a dep so the admin bundle stays trivially small.
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unitIdx = 0;
  while (value >= 1024 && unitIdx < units.length - 1) {
    value /= 1024;
    unitIdx += 1;
  }
  const fixed = value < 10 && unitIdx > 0 ? value.toFixed(1) : Math.round(value).toString();
  return `${fixed} ${units[unitIdx]}`;
}

function summarizeError(error?: string): string {
  if (!error) return '';
  if (error.startsWith('source not configured')) return 'Source not configured.';
  return error;
}

function isUnknown(error?: string): boolean {
  return Boolean(error && error.startsWith('source not configured'));
}

export function deriveDatabase(db: DatabaseStatus): CardData {
  if (isUnknown(db.error)) {
    return {
      tone: 'unknown',
      summary: 'Source not configured.',
      rows: [],
    };
  }
  if (!db.ok || db.error) {
    return {
      tone: 'error',
      errorMessage: db.error || 'Database is unreachable.',
      rows: [
        { label: 'Max conns', value: db.max_conns.toString() },
        { label: 'In use', value: db.in_use.toString() },
      ],
    };
  }
  // Warn when in_use crosses 80% of max_conns — close enough to the
  // ceiling that one slow query plus a traffic burst could starve the
  // pool. Operators usually want to scale before that point.
  const ratio = db.max_conns > 0 ? db.in_use / db.max_conns : 0;
  const tone: StatusTone = ratio >= 0.8 ? 'warn' : 'ok';
  const summary =
    tone === 'warn'
      ? `Pool utilization ${Math.round(ratio * 100)}% — consider scaling.`
      : undefined;
  return {
    tone,
    summary,
    rows: [
      { label: 'Version', value: db.version ?? '—' },
      { label: 'Max conns', value: db.max_conns.toString() },
      { label: 'In use', value: db.in_use.toString() },
      { label: 'Idle', value: db.idle.toString() },
      { label: 'Ping', value: `${db.response_time_ms} ms` },
    ],
  };
}

export function deriveRedis(rds: RedisStatus): CardData {
  if (isUnknown(rds.error)) {
    return { tone: 'unknown', summary: 'Source not configured.', rows: [] };
  }
  if (!rds.ok || rds.error) {
    return {
      tone: 'error',
      errorMessage: rds.error || 'Redis is unreachable.',
      rows: [],
    };
  }
  return {
    tone: 'ok',
    rows: [
      { label: 'Version', value: rds.version ?? '—' },
      { label: 'Ping', value: `${rds.response_time_ms} ms` },
    ],
  };
}

export function deriveMigrations(m: MigrationsStatus): CardData {
  if (isUnknown(m.error)) {
    return { tone: 'unknown', summary: 'Source not configured.', rows: [] };
  }
  if (m.error) {
    return {
      tone: 'error',
      errorMessage: m.error,
      rows: [
        { label: 'Current', value: m.current_version.toString() },
        { label: 'Bundled', value: m.total_count.toString() },
      ],
    };
  }
  // Dirty is *always* an error — schema_migrations.dirty=true means
  // a prior migration crashed mid-statement and the operator must
  // either roll forward or call `migrate force`.
  if (m.dirty) {
    return {
      tone: 'error',
      errorMessage:
        'Migration row is marked dirty — a prior migration left the schema inconsistent. Resolve with `gonext migrate force`.',
      rows: [
        { label: 'Current', value: m.current_version.toString() },
        { label: 'Bundled', value: m.total_count.toString() },
      ],
    };
  }
  // Bundled > current is "pending migrations"; not necessarily bad on
  // a freshly-deployed binary but worth a warn so the operator knows
  // a `make migrate` is outstanding.
  const pending = m.total_count - Number(m.current_version);
  const tone: StatusTone = pending > 0 ? 'warn' : 'ok';
  const summary = pending > 0 ? `${pending} migration(s) pending.` : undefined;
  return {
    tone,
    summary,
    rows: [
      { label: 'Current', value: m.current_version.toString() },
      { label: 'Bundled', value: m.total_count.toString() },
      { label: 'Dirty', value: m.dirty ? 'yes' : 'no' },
    ],
  };
}

export function deriveTheme(t: ThemeStatus): CardData {
  if (isUnknown(t.error)) {
    return { tone: 'unknown', summary: 'Source not configured.', rows: [] };
  }
  if (t.error) {
    return {
      tone: 'error',
      errorMessage: t.error,
      rows: t.active_name ? [{ label: 'Active', value: t.active_name }] : [],
    };
  }
  return {
    tone: 'ok',
    rows: [
      { label: 'Active', value: t.active_name || '—' },
      { label: 'Manifest', value: t.version || '—' },
      { label: 'Parts', value: t.parts_count.toString() },
      { label: 'Templates', value: t.templates_count.toString() },
    ],
  };
}

export function derivePlugins(p: PluginsStatus): CardData {
  if (isUnknown(p.error)) {
    return { tone: 'unknown', summary: 'Source not configured.', rows: [] };
  }
  if (p.error) {
    return { tone: 'error', errorMessage: p.error, rows: [] };
  }
  // Any errored plugin makes the section red — operators want to see
  // that there's something to fix, not have it folded into a green
  // overall result.
  const tone: StatusTone = p.errored > 0 ? 'error' : 'ok';
  const summary =
    tone === 'error'
      ? `${p.errored} plugin(s) in errored state — visit Plugins to inspect.`
      : undefined;
  return {
    tone,
    summary,
    rows: [
      { label: 'Installed', value: p.installed.toString() },
      { label: 'Active', value: p.active.toString() },
      { label: 'Errored', value: p.errored.toString() },
      { label: 'Last install', value: p.last_install || '—' },
    ],
  };
}

export function deriveDisk(d: DiskStatus): CardData {
  if (isUnknown(d.error)) {
    return { tone: 'unknown', summary: 'Source not configured.', rows: [] };
  }
  if (d.error) {
    return {
      tone: 'warn',
      errorMessage: d.error,
      rows: [
        { label: 'Theme dir', value: formatBytes(d.theme_dir_bytes) },
        { label: 'Media dir', value: formatBytes(d.media_dir_bytes) },
      ],
    };
  }
  return {
    tone: 'ok',
    rows: [
      { label: 'Theme dir', value: formatBytes(d.theme_dir_bytes) },
      { label: 'Media dir', value: formatBytes(d.media_dir_bytes) },
    ],
  };
}

export function deriveQueue(q: QueueStatus): CardData {
  if (q.error) {
    return {
      tone: 'error',
      errorMessage: q.error,
      rows: [
        { label: 'Pending', value: q.pending.toString() },
        { label: 'Active', value: q.active.toString() },
      ],
    };
  }
  // Warn when the failure rate over today's window crosses 5% — a
  // low-bar threshold operators tune in their alerting layer later.
  const denom = q.processed_24h + q.failed_24h;
  const failRate = denom > 0 ? q.failed_24h / denom : 0;
  let tone: StatusTone = 'ok';
  let summary: string | undefined;
  if (failRate >= 0.05) {
    tone = 'warn';
    summary = `${Math.round(failRate * 100)}% failure rate over the last 24h.`;
  } else if (q.pending > 1000) {
    tone = 'warn';
    summary = `${q.pending.toLocaleString()} tasks pending — queue is backed up.`;
  }
  return {
    tone,
    summary,
    rows: [
      { label: 'Pending', value: q.pending.toString() },
      { label: 'Active', value: q.active.toString() },
      { label: 'Processed (24h)', value: q.processed_24h.toLocaleString() },
      { label: 'Failed (24h)', value: q.failed_24h.toLocaleString() },
    ],
  };
}

export function deriveBuildInfo(report: StatusReport): CardData {
  return {
    tone: 'ok',
    rows: [
      { label: 'Version', value: report.version || '—' },
      { label: 'Commit', value: shortCommit(report.commit) },
      { label: 'Built', value: report.build_date || '—' },
      { label: 'Go', value: report.go_version || '—' },
      { label: 'Platform', value: `${report.os}/${report.arch}` },
    ],
  };
}

function shortCommit(commit: string): string {
  if (!commit) return '—';
  if (commit.length <= 12) return commit;
  return commit.slice(0, 12);
}

/**
 * Produce a redacted copy of the report for the "Copy diagnostic"
 * button. The current cut redacts nothing — every field in StatusReport
 * is already operator-safe (no DSNs, no API keys, no PII). The function
 * exists so a future field that would carry secrets (e.g. a queue task
 * payload preview) can be stripped here without changing call sites.
 */
export function redactDiagnostic(report: StatusReport): string {
  return JSON.stringify(report, null, 2);
}
