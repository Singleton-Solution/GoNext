package status

// StatusReport is the wire shape returned by GET /api/v1/admin/status.
//
// The JSON field names are part of the API contract — the admin Status
// page parses them by hand and the redacted-diagnostic clipboard dump
// quotes them verbatim into support tickets. Add fields freely; renaming
// or removing fields is a breaking change.
//
// Every sub-section embeds an Error string that the aggregator fills
// when the corresponding source returned an error. A non-empty Error
// turns the section's traffic-light to red in the UI without taking
// down the whole report — one bad axis (e.g. Redis blip) should not
// erase the operator's view of the other seven.
type StatusReport struct {
	// Version, Commit, BuildDate identify the running binary. Sourced
	// from packages/go/buildinfo. Stable across the process lifetime;
	// the values come from -ldflags at release time or from
	// debug.BuildInfo on a go-build-from-source run.
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`

	// Generated is the RFC3339 timestamp the report was aggregated at.
	// The admin UI uses this to render "last refreshed Xs ago" and to
	// distinguish a stale cached report from a fresh one.
	Generated string `json:"generated"`

	Database   DatabaseStatus   `json:"database"`
	Redis      RedisStatus      `json:"redis"`
	Migrations MigrationsStatus `json:"migrations"`
	Queues     []QueueStatus    `json:"queues"`
	Theme      ThemeStatus      `json:"theme"`
	Plugins    PluginsStatus    `json:"plugins"`
	Disk       DiskStatus       `json:"disk"`
}

// DatabaseStatus reports the pgxpool stats + a ping round-trip + the
// Postgres server_version. OK is true when Ping returned nil; on a
// failure Error carries the operator-readable reason and the pool
// counters fall back to zero (which the UI renders as "—").
type DatabaseStatus struct {
	OK             bool   `json:"ok"`
	Version        string `json:"version,omitempty"`
	MaxConns       int32  `json:"max_conns"`
	InUse          int32  `json:"in_use"`
	Idle           int32  `json:"idle"`
	ResponseTimeMS int64  `json:"response_time_ms"`
	Error          string `json:"error,omitempty"`
}

// RedisStatus reports the redis_version (parsed out of the INFO server
// reply) plus a ping round-trip. Same OK/Error convention as Database.
type RedisStatus struct {
	OK             bool   `json:"ok"`
	Version        string `json:"version,omitempty"`
	ResponseTimeMS int64  `json:"response_time_ms"`
	Error          string `json:"error,omitempty"`
}

// MigrationsStatus reports the current schema_migrations row plus the
// total number of .up.sql files bundled in the migrations directory.
// A non-zero CurrentVersion strictly less than TotalCount means the
// process booted against an older schema than ships in this binary —
// usually a partial deploy or a missed `make migrate`. Dirty=true means
// a prior migration crashed mid-statement; the row needs `force`.
type MigrationsStatus struct {
	CurrentVersion uint   `json:"current_version"`
	Dirty          bool   `json:"dirty"`
	TotalCount     int    `json:"total_count"`
	Error          string `json:"error,omitempty"`
}

// QueueStatus reports per-queue counters straight off the Asynq
// Inspector. Pending and Active are point-in-time depths; Processed24H
// and Failed24H are the running-day counters that asynq itself
// maintains (resets at midnight UTC; the field name reflects intent,
// not a sliding window).
type QueueStatus struct {
	Name         string `json:"name"`
	Pending      int    `json:"pending"`
	Active       int    `json:"active"`
	Processed24H int    `json:"processed_24h"`
	Failed24H    int    `json:"failed_24h"`
	Error        string `json:"error,omitempty"`
}

// ThemeStatus reports the active theme's identity + a coarse measure
// of its surface area (named template parts + custom templates). The
// counts come from the in-memory ThemeJSON; we don't enumerate the
// actual files because the renderer's template cache is the source of
// truth and is not exposed here.
type ThemeStatus struct {
	ActiveName     string `json:"active_name"`
	Version        string `json:"version,omitempty"`
	PartsCount     int    `json:"parts_count"`
	TemplatesCount int    `json:"templates_count"`
	Error          string `json:"error,omitempty"`
}

// PluginsStatus reports a tally over lifecycle.Storage.List. The four
// counters partition the row set (installed = total; active + errored
// + (inactive+pending_uninstall) sum to installed). LastInstall is the
// most recent InstalledAt across all rows; an empty string means no
// plugin has ever been installed on this site.
type PluginsStatus struct {
	Installed   int    `json:"installed"`
	Active      int    `json:"active"`
	Errored     int    `json:"errored"`
	LastInstall string `json:"last_install,omitempty"`
	Error       string `json:"error,omitempty"`
}

// DiskStatus reports the on-disk byte counts for the theme directory
// and the media directory. The walk is performed inline on each
// request — there is no caching — because the operator using System
// Status is rarely the same person triggering large uploads, so the
// latency of a tree walk over a hundred-MB media directory is fine.
// If a future site grows to tens of GB and walks get slow, a daily
// snapshot stored in Redis behind a TTL is the standard fix.
type DiskStatus struct {
	ThemeDirBytes int64  `json:"theme_dir_bytes"`
	MediaDirBytes int64  `json:"media_dir_bytes"`
	Error         string `json:"error,omitempty"`
}
