package status

import (
	"context"
	"time"
)

// Sources is the bag of data accessors the handler reads from. Each
// field is an interface so tests stub one axis at a time without
// dragging in Postgres / Redis / Asynq / a real plugin registry.
//
// Production wiring (apps/api/cmd/server/main.go) adapts the concrete
// types to these interfaces via tiny shims:
//
//   - DB:         pgxpoolSource{pool}      → *pgxpool.Pool
//   - Redis:      redisClientSource{rdb}   → *redis.Client
//   - Migrations: migrateDirSource{dir, cfg}
//   - Queues:     asynqInspectorSource{insp}
//   - Theme:      themeRegistrySource{reg}
//   - Plugins:    lifecycleStorageSource{store}
//   - Disk:       fsSource{themeDir, mediaDir}
//
// A nil field is permissible: the aggregator records "source not
// configured" as the section's Error and the UI shows the axis as
// unknown rather than red. This keeps the page useful on a developer
// laptop where (say) Redis isn't running.
type Sources struct {
	BuildInfo  BuildInfoSource
	DB         DatabaseSource
	Redis      RedisSource
	Migrations MigrationsSource
	Queues     QueueSource
	Theme      ThemeSource
	Plugins    PluginsSource
	Disk       DiskSource
}

// BuildInfoSource returns the running binary's identity. Production
// wiring trivially delegates to buildinfo.Get("api"); tests use a
// fixed stub so the report is deterministic.
type BuildInfoSource interface {
	Get() BuildInfoSnapshot
}

// BuildInfoSnapshot is the minimal slice of buildinfo.Info the report
// needs. We define it here rather than importing the buildinfo struct
// so the status package stays decoupled from the linker-injected
// surface — tests don't need to know about debug.ReadBuildInfo.
type BuildInfoSnapshot struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	OS        string
	Arch      string
}

// DatabaseSource probes the application's primary Postgres connection.
// Snapshot is expected to ping the pool, scrape its connection stats,
// and read server_version. Implementations should observe the ctx
// deadline; the aggregator imposes a per-source budget around 2s.
type DatabaseSource interface {
	Snapshot(ctx context.Context) DatabaseStatus
}

// RedisSource probes the application's Redis client. Snapshot is
// expected to call PING for the round-trip measurement and pull the
// server's redis_version out of an INFO server reply.
type RedisSource interface {
	Snapshot(ctx context.Context) RedisStatus
}

// MigrationsSource reports the migration state. Snapshot reads the
// schema_migrations row plus the count of .up.sql files in the bundled
// migrations directory; the difference between the two is what the UI
// surfaces as "pending migrations".
type MigrationsSource interface {
	Snapshot(ctx context.Context) MigrationsStatus
}

// QueueSource yields the per-queue counters for every queue the
// Asynq Inspector knows about. The aggregator imposes no ordering;
// the source returns the slice in a stable order so the report's
// JSON is deterministic across requests (the admin UI iterates and
// renders one card per queue without sorting).
type QueueSource interface {
	Snapshot(ctx context.Context) []QueueStatus
}

// ThemeSource returns the identity + surface counts of the active
// theme. Snapshot does not enumerate the renderer's template cache —
// it consults the parsed theme.json and reports parts_count +
// custom_templates count directly.
type ThemeSource interface {
	Snapshot(ctx context.Context) ThemeStatus
}

// PluginsSource walks lifecycle.Storage.List and tallies the rows by
// state. The most recent InstalledAt is reported as LastInstall.
type PluginsSource interface {
	Snapshot(ctx context.Context) PluginsStatus
}

// DiskSource reports the on-disk byte counts for the theme and media
// directories. A walk is performed inline; the aggregator's per-source
// budget bounds it.
type DiskSource interface {
	Snapshot(ctx context.Context) DiskStatus
}

// Clock is the time abstraction used for the report's "generated"
// timestamp. Production passes time.Now; tests pass a fixed time so the
// JSON is byte-deterministic.
type Clock func() time.Time
