/**
 * Wire types for the System Status report.
 *
 * The field names match the Go server's `internal/admin/status.StatusReport`
 * struct tags byte-for-byte. There is no shared codegen yet (#214's
 * deliverable); duplicating the shape here keeps the page typed without
 * dragging in the API package. When the OpenAPI client lands these get
 * replaced by generated types.
 */
export interface StatusReport {
  version: string;
  commit: string;
  build_date: string;
  go_version: string;
  os: string;
  arch: string;
  generated: string;

  database: DatabaseStatus;
  redis: RedisStatus;
  migrations: MigrationsStatus;
  queues: QueueStatus[];
  theme: ThemeStatus;
  plugins: PluginsStatus;
  disk: DiskStatus;
}

export interface DatabaseStatus {
  ok: boolean;
  version?: string;
  max_conns: number;
  in_use: number;
  idle: number;
  response_time_ms: number;
  error?: string;
}

export interface RedisStatus {
  ok: boolean;
  version?: string;
  response_time_ms: number;
  error?: string;
}

export interface MigrationsStatus {
  current_version: number;
  dirty: boolean;
  total_count: number;
  error?: string;
}

export interface QueueStatus {
  name: string;
  pending: number;
  active: number;
  processed_24h: number;
  failed_24h: number;
  error?: string;
}

export interface ThemeStatus {
  active_name: string;
  version?: string;
  parts_count: number;
  templates_count: number;
  error?: string;
}

export interface PluginsStatus {
  installed: number;
  active: number;
  errored: number;
  last_install?: string;
  error?: string;
}

export interface DiskStatus {
  theme_dir_bytes: number;
  media_dir_bytes: number;
  error?: string;
}

/**
 * Traffic-light state used by every card. The mapping is local to each
 * card's heuristic — see StatusCard for the per-section logic — so the
 * report itself doesn't have to pick a color (the server avoids picking
 * "red" for a transient blip; the UI does that judgment).
 */
export type StatusTone = 'ok' | 'warn' | 'error' | 'unknown';
