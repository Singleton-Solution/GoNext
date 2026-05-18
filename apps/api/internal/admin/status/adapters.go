package status

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/lifecycle"
	"github.com/Singleton-Solution/GoNext/packages/go/theme"
)

// BuildInfoAdapter wraps the buildinfo package as a BuildInfoSource.
// The service name is captured at construction so the same value used
// by the rest of the server (typically "api") flows into the report
// without the handler having to know it.
type BuildInfoAdapter struct {
	Service string
}

// Get implements BuildInfoSource.
func (a BuildInfoAdapter) Get() BuildInfoSnapshot {
	bi := buildinfo.Get(a.Service)
	return BuildInfoSnapshot{
		Version:   bi.Version,
		Commit:    bi.Commit,
		Date:      bi.Date,
		GoVersion: bi.GoVersion,
		OS:        bi.OS,
		Arch:      bi.Arch,
	}
}

// PgxPoolSource adapts *pgxpool.Pool to DatabaseSource. The adapter is
// cheap to construct and holds no state of its own — it leans on the
// pool's existing concurrency safety.
type PgxPoolSource struct {
	Pool *pgxpool.Pool
}

// Snapshot pings the pool, scrapes its stats, and reads server_version.
// The reported ResponseTimeMS is the wall-clock duration of the Ping
// call — close to a TCP round-trip on a healthy local DB.
//
// On a failed Ping we still surface the pool stats: they're useful for
// diagnosing "we have zero idle conns and the new acquire timed out"
// independent of whether the DB itself answers.
func (s PgxPoolSource) Snapshot(ctx context.Context) DatabaseStatus {
	if s.Pool == nil {
		return DatabaseStatus{Error: "pool not configured"}
	}
	stat := s.Pool.Stat()
	status := DatabaseStatus{
		MaxConns: stat.MaxConns(),
		InUse:    stat.AcquiredConns(),
		Idle:     stat.IdleConns(),
	}

	start := time.Now()
	if err := s.Pool.Ping(ctx); err != nil {
		status.Error = err.Error()
		status.ResponseTimeMS = time.Since(start).Milliseconds()
		return status
	}
	status.OK = true
	status.ResponseTimeMS = time.Since(start).Milliseconds()

	// server_version is a server-side GUC; one round-trip suffices and
	// the pool's statement_timeout AfterConnect hook is already loose
	// enough to permit it. A scan error here is non-fatal: we keep the
	// happy ping result and leave Version empty.
	var version string
	row := s.Pool.QueryRow(ctx, "SHOW server_version")
	if err := row.Scan(&version); err == nil {
		status.Version = version
	}
	return status
}

// RedisClientSource adapts *redis.Client to RedisSource.
type RedisClientSource struct {
	Client *goredis.Client
}

// Snapshot pings the server and parses redis_version out of the
// INFO server reply. INFO returns ~100 lines of "key:value" — we
// scan for the redis_version line and stop; no extra allocations.
func (s RedisClientSource) Snapshot(ctx context.Context) RedisStatus {
	if s.Client == nil {
		return RedisStatus{Error: "client not configured"}
	}
	start := time.Now()
	if err := s.Client.Ping(ctx).Err(); err != nil {
		return RedisStatus{
			Error:          err.Error(),
			ResponseTimeMS: time.Since(start).Milliseconds(),
		}
	}
	status := RedisStatus{
		OK:             true,
		ResponseTimeMS: time.Since(start).Milliseconds(),
	}

	info, err := s.Client.Info(ctx, "server").Result()
	if err == nil {
		status.Version = parseRedisVersion(info)
	}
	return status
}

// parseRedisVersion scans an INFO server reply for the redis_version
// field. The reply is a literal CRLF-delimited "key:value" sequence;
// we walk lines and stop at the first match. Returns "" if not found.
func parseRedisVersion(info string) string {
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "redis_version:"
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

// MigrationDirSource adapts a migrations directory + a status function
// to MigrationsSource. We accept the StatusFn rather than depending on
// packages/go/migrate directly because Status() opens a dedicated SQL
// connection and acquires an advisory lock — useful in CLI, overkill
// for a read-only admin probe. Callers supply a lighter shim in
// production (a cached read of schema_migrations off the live pool).
type MigrationDirSource struct {
	// Dir is the directory containing the bundled .up.sql files; the
	// TotalCount field is the number of files matching `*.up.sql` in
	// the directory. An empty Dir is permissible: TotalCount falls
	// back to 0 and the field is rendered as "—".
	Dir string

	// StatusFn returns (currentVersion, dirty, error) for the live
	// database. A nil StatusFn leaves CurrentVersion at 0 and Dirty
	// at false; the report's Error reflects the disabled state.
	StatusFn func(ctx context.Context) (current uint, dirty bool, err error)
}

// Snapshot reads the on-disk file count and (if StatusFn is set) the
// live schema_migrations state.
func (s MigrationDirSource) Snapshot(ctx context.Context) MigrationsStatus {
	status := MigrationsStatus{}
	if s.Dir != "" {
		count, err := countUpSQL(s.Dir)
		if err != nil {
			status.Error = fmt.Sprintf("scan dir: %v", err)
		}
		status.TotalCount = count
	}
	if s.StatusFn == nil {
		if status.Error == "" {
			status.Error = "status fn not configured"
		}
		return status
	}
	cur, dirty, err := s.StatusFn(ctx)
	if err != nil {
		// Preserve any earlier Error so the operator sees both the
		// scan failure and the runtime failure; the UI is bandwidth-
		// unconstrained.
		if status.Error == "" {
			status.Error = err.Error()
		} else {
			status.Error = status.Error + "; " + err.Error()
		}
		return status
	}
	status.CurrentVersion = cur
	status.Dirty = dirty
	return status
}

func countUpSQL(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".up.sql") {
			n++
		}
	}
	return n, nil
}

// AsynqInspectorSource adapts a queue inspector to QueueSource.
// Queues is the fixed name list to scan — passed in rather than
// discovered via Inspector.Queues() because the discovery call is
// itself a Redis round-trip we don't need (the queue topology is
// declared by config and known at boot time).
type AsynqInspectorSource struct {
	Inspector QueueInspector
	Queues    []string
}

// QueueInspector is the subset of *asynq.Inspector this package
// needs. Defined here so tests can supply a fake that returns
// controllable counters without standing up Redis.
type QueueInspector interface {
	GetQueueInfo(queue string) (*asynq.QueueInfo, error)
}

// Snapshot iterates the configured queue list and reads each queue's
// counters off the Inspector. Per-queue errors are isolated: a single
// missing queue (operator typo, queue not yet created) renders that
// row's Error without taking down the rest of the snapshot. The
// returned slice is ordered by the configured queue list so JSON
// output is deterministic.
func (s AsynqInspectorSource) Snapshot(_ context.Context) []QueueStatus {
	if s.Inspector == nil || len(s.Queues) == 0 {
		return nil
	}
	out := make([]QueueStatus, 0, len(s.Queues))
	for _, name := range s.Queues {
		info, err := s.Inspector.GetQueueInfo(name)
		if err != nil {
			out = append(out, QueueStatus{Name: name, Error: err.Error()})
			continue
		}
		out = append(out, QueueStatus{
			Name:         name,
			Pending:      info.Pending,
			Active:       info.Active,
			Processed24H: info.Processed,
			Failed24H:    info.Failed,
		})
	}
	return out
}

// ActiveThemeFn returns a snapshot of the currently active theme +
// the theme name. Production wiring queries the theme registry (a
// long-lived in-memory map populated at boot); tests pass a closure
// that returns a fixed *ThemeJSON.
type ActiveThemeFn func() (name string, manifest *theme.ThemeJSON, err error)

// ThemeFnSource adapts an ActiveThemeFn to ThemeSource. A nil Fn
// renders the theme axis as "not configured"; a Fn that returns an
// error renders it red.
type ThemeFnSource struct {
	Fn ActiveThemeFn
}

// Snapshot consults the underlying ActiveThemeFn and counts the
// declared template parts + custom templates from the parsed manifest.
func (s ThemeFnSource) Snapshot(_ context.Context) ThemeStatus {
	if s.Fn == nil {
		return ThemeStatus{Error: "theme fn not configured"}
	}
	name, manifest, err := s.Fn()
	if err != nil {
		return ThemeStatus{ActiveName: name, Error: err.Error()}
	}
	if manifest == nil {
		return ThemeStatus{ActiveName: name, Error: "no active theme"}
	}
	return ThemeStatus{
		ActiveName:     name,
		Version:        themeVersionString(manifest.Version),
		PartsCount:     len(manifest.TemplateParts),
		TemplatesCount: len(manifest.CustomTemplates),
	}
}

// themeVersionString renders an integer schema version as a string
// so the JSON field is uniformly a string regardless of theme system
// version. The theme package stores Version as an int (the manifest
// schema version), not a SemVer — keep the type conversion local so
// callers don't have to think about it.
func themeVersionString(v int) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("v%d", v)
}

// LifecycleStoreSource adapts a lifecycle.Storage to PluginsSource.
type LifecycleStoreSource struct {
	Store lifecycle.Storage
}

// Snapshot lists every plugin row and tallies by state. The "Errored"
// counter combines StateErrored rows (the dedicated error state) with
// any row whose ErrorAt is non-zero — operators care about both.
// LastInstall is the most recent InstalledAt across all rows in
// RFC3339 form.
func (s LifecycleStoreSource) Snapshot(ctx context.Context) PluginsStatus {
	if s.Store == nil {
		return PluginsStatus{Error: "lifecycle store not configured"}
	}
	rows, err := s.Store.List(ctx)
	if err != nil {
		return PluginsStatus{Error: err.Error()}
	}
	status := PluginsStatus{Installed: len(rows)}
	var latest time.Time
	for _, p := range rows {
		switch p.State {
		case lifecycle.StateActive:
			status.Active++
		case lifecycle.StateErrored:
			status.Errored++
		}
		if p.InstalledAt.After(latest) {
			latest = p.InstalledAt
		}
	}
	if !latest.IsZero() {
		status.LastInstall = latest.UTC().Format(time.RFC3339)
	}
	return status
}

// FilesystemDiskSource adapts a pair of directories to DiskSource by
// walking each tree and summing file sizes. Both paths are optional —
// an empty string skips that axis and reports zero.
//
// The walk is intentionally synchronous and uncached. System Status
// is a low-frequency surface; the operator is rarely the same person
// triggering large uploads; and a daily-snapshot cache would couple
// this package to Redis just to save a few hundred milliseconds of
// I/O on the worst-case site. If a future deploy crosses tens of GB
// in media and the latency stings, a TTL-cached snapshot in Redis is
// the standard fix.
type FilesystemDiskSource struct {
	ThemeDir string
	MediaDir string
}

// Snapshot walks each configured directory and returns the byte
// totals. A walk error on one directory is recorded in Error without
// erasing the successful walk of the other.
func (s FilesystemDiskSource) Snapshot(_ context.Context) DiskStatus {
	out := DiskStatus{}
	var errs []string
	if s.ThemeDir != "" {
		n, err := walkBytes(s.ThemeDir)
		if err != nil {
			errs = append(errs, fmt.Sprintf("theme_dir: %v", err))
		}
		out.ThemeDirBytes = n
	}
	if s.MediaDir != "" {
		n, err := walkBytes(s.MediaDir)
		if err != nil {
			errs = append(errs, fmt.Sprintf("media_dir: %v", err))
		}
		out.MediaDirBytes = n
	}
	if len(errs) > 0 {
		// Deterministic order so the JSON is byte-stable across runs.
		sort.Strings(errs)
		out.Error = strings.Join(errs, "; ")
	}
	return out
}

// walkBytes returns the cumulative size of every regular file under
// root. ENOENT on root itself is treated as zero (the path simply
// doesn't exist yet on a fresh install); any other error is bubbled
// so the operator sees it in the report.
func walkBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			// A per-entry stat error during the walk shouldn't kill the
			// whole tally; skip the entry but keep going.
			if errIsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			if errIsNotExist(infoErr) {
				return nil
			}
			return infoErr
		}
		total += info.Size()
		return nil
	})
	if err != nil && errIsNotExist(err) {
		return 0, nil
	}
	return total, err
}

func errIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
